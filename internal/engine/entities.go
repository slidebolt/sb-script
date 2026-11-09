package engine

// Scripts and automations as switch entities.
//
// Every definition gets switch entities owned by sb-script:
//
//	script, no scopes     sb-script.script-<name>.run
//	script, with scopes   sb-script.script-<name>.<scope>   (one per scope)
//	automation            sb-script.automation-<name>.enabled
//
// A script switch is on while its instance (name + scope) runs: turning it on
// starts the instance, turning it off stops it gracefully (the Script's stop
// option runs first). Hard stops (ctx.scripts:stop, stopAll, the API, a script
// stopping itself) flip it off too.
//
// An automation switch is "enabled": off stops the automation and keeps it
// stopped across restarts and definition saves; on starts it again.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	scriptstore "github.com/slidebolt/sb-script/internal/store"
	lua "github.com/yuin/gopher-lua"
)

const (
	entityPlugin     = "sb-script"
	scriptDevicePfx  = "script-"
	autoDevicePfx    = "automation-"
	unscopedEntityID = "run"
	enabledEntityID  = "enabled"
	gracefulMaxWait  = 60 * time.Second
	describeTimeout  = 2 * time.Second
)

var switchCommands = []string{"switch_turn_on", "switch_turn_off", "switch_toggle"}

type binding struct {
	kind  string // "script" or "automation"
	name  string // definition name
	scope string // queryRef the instance runs on ("" = unscoped)
}

// scriptMeta is what the entity's state says besides power. It is stored in
// the state next to power (meta is the user's sidecar and plugin saves drop
// it); consumers that read the state as a domain.Switch see only power.
type scriptMeta struct {
	Running   bool       `json:"running"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	LastError string     `json:"lastError,omitempty"`
}

type scriptState struct {
	Power bool `json:"power"`
	scriptMeta
}

func deviceID(kind, name string) string {
	if kind == "automation" {
		return autoDevicePfx + strings.ToLower(name)
	}
	return scriptDevicePfx + strings.ToLower(name)
}

func (b binding) key() domain.EntityKey {
	id := b.scope
	switch {
	case b.kind == "automation":
		id = enabledEntityID
	case id == "":
		id = unscopedEntityID
	}
	return domain.EntityKey{Plugin: entityPlugin, DeviceID: deviceID(b.kind, b.name), ID: id}
}

// declaration is what a definition's source says about itself.
type declaration struct {
	kind   string
	scopes []string
}

// describe loads source in a throwaway VM whose Script and Automation only
// record their arguments; no callback runs and nothing is subscribed.
func describe(name, source string) (declaration, error) {
	L := lua.NewState()
	defer L.Close()
	ctx, cancel := context.WithTimeout(context.Background(), describeTimeout)
	defer cancel()
	L.SetContext(ctx)

	var decl declaration
	marker := func(kind string) *lua.LFunction {
		return L.NewFunction(func(L *lua.LState) int {
			tbl := L.NewTable()
			L.SetField(tbl, "kind", lua.LString(kind))
			L.Push(tbl)
			return 1
		})
	}
	for _, g := range []string{"Entity", "Query", "QueryRef", "None", "Interval", "Cron"} {
		L.SetGlobal(g, marker(strings.ToLower(g)))
	}
	L.SetGlobal("Automation", L.NewFunction(func(L *lua.LState) int {
		if L.CheckString(1) == name {
			decl.kind = "automation"
		}
		return 0
	}))
	L.SetGlobal("Script", L.NewFunction(func(L *lua.LState) int {
		if L.CheckString(1) != name {
			return 0
		}
		decl.kind = "script"
		if opts, ok := L.Get(2).(*lua.LTable); ok {
			if scopes, ok := L.GetField(opts, "scopes").(*lua.LTable); ok {
				scopes.ForEach(func(_, v lua.LValue) {
					if s, ok := v.(lua.LString); ok && s != "" {
						decl.scopes = append(decl.scopes, string(s))
					}
				})
			}
		}
		return 0
	}))
	if err := L.DoString(source); err != nil {
		return declaration{}, err
	}
	decl.scopes = dedupeStrings(decl.scopes)
	return decl, nil
}

// describeBindings works out a definition's switch entities and records them.
// It saves nothing; syncDefinitionEntities does.
func (e *Engine) describeBindings(name, source string) {
	decl, err := describe(name, source)
	if err != nil {
		slog.Warn("sb-script: describe definition", "name", name, "err", err)
	}
	var bs []binding
	switch decl.kind {
	case "automation":
		bs = []binding{{kind: "automation", name: name}}
	case "script":
		if len(decl.scopes) == 0 {
			bs = []binding{{kind: "script", name: name}}
		}
		for _, scope := range decl.scopes {
			bs = append(bs, binding{kind: "script", name: name, scope: scope})
		}
	}

	e.mu.Lock()
	old := e.defBindings[name]
	keys := make([]string, 0, len(bs))
	for _, b := range bs {
		k := b.key().Key()
		keys = append(keys, k)
		e.bindings[k] = b
	}
	e.defBindings[name] = keys
	var stale []string
	for _, k := range old {
		if !containsString(keys, k) {
			stale = append(stale, k)
			delete(e.bindings, k)
		}
	}
	e.mu.Unlock()
	for _, k := range stale {
		e.deleteEntityKey(k)
	}
}

func (e *Engine) bindingsFor(name string) []binding {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]binding, 0, len(e.defBindings[name]))
	for _, k := range e.defBindings[name] {
		out = append(out, e.bindings[k])
	}
	return out
}

func (e *Engine) bindingFor(kind, name, scope string) (binding, bool) {
	b := binding{kind: kind, name: name, scope: scope}
	e.mu.RLock()
	defer e.mu.RUnlock()
	got, ok := e.bindings[b.key().Key()]
	return got, ok && got.name == name
}

func (e *Engine) running(name, scope string) (*time.Time, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	inst, ok := e.instances[scopeHash(name, scope)]
	if !ok {
		return nil, false
	}
	return inst.record.StartedAt, true
}

// loadEntity returns the stored entity's power and meta, if it exists.
func (e *Engine) loadEntity(k domain.EntityKey) (bool, scriptMeta, bool) {
	raw, err := e.store.Get(k)
	if err != nil {
		return false, scriptMeta{}, false
	}
	var ent struct {
		State scriptState `json:"state"`
	}
	if json.Unmarshal(raw, &ent) != nil {
		return false, scriptMeta{}, false
	}
	return ent.State.Power, ent.State.scriptMeta, true
}

// automationEnabled reports whether an automation may run: true unless its
// enabled switch exists and is off.
func (e *Engine) automationEnabled(name string) bool {
	power, _, ok := e.loadEntity(binding{kind: "automation", name: name}.key())
	return !ok || power
}

// saveBinding writes a binding's device and entity, skipping writes that would
// not change anything (each write is a state change scripts can trigger on).
func (e *Engine) saveBinding(b binding, power bool, meta scriptMeta) {
	k := b.key()
	if oldPower, oldMeta, ok := e.loadEntity(k); ok && oldPower == power && sameMeta(oldMeta, meta) {
		return
	}
	name := b.name
	if b.scope != "" {
		name = b.name + " (" + b.scope + ")"
	}
	ent := domain.Entity{
		ID: k.ID, Plugin: k.Plugin, DeviceID: k.DeviceID, Type: "switch", Name: name,
		Commands: switchCommands,
		State:    scriptState{Power: power, scriptMeta: meta},
	}
	if err := e.store.Save(domain.Device{ID: k.DeviceID, Plugin: entityPlugin, Name: b.name}); err != nil {
		slog.Warn("sb-script: save script device", "key", k.Key(), "err", err)
	}
	if err := e.store.Save(ent); err != nil {
		slog.Warn("sb-script: save script entity", "key", k.Key(), "err", err)
	}
}

func sameMeta(a, b scriptMeta) bool {
	if a.Running != b.Running || a.LastError != b.LastError {
		return false
	}
	if (a.StartedAt == nil) != (b.StartedAt == nil) {
		return false
	}
	return a.StartedAt == nil || a.StartedAt.Equal(*b.StartedAt)
}

// syncBinding writes the entity from what is actually running.
func (e *Engine) syncBinding(b binding) {
	_, oldMeta, _ := e.loadEntity(b.key())
	scope := b.scope
	started, running := e.running(b.name, scope)
	meta := scriptMeta{Running: running, StartedAt: started}
	if running {
		meta.LastError = oldMeta.LastError
	}
	power := running
	if b.kind == "automation" {
		power = e.automationEnabled(b.name)
	}
	e.saveBinding(b, power, meta)
}

func (e *Engine) syncDefinitionEntities(name string) {
	for _, b := range e.bindingsFor(name) {
		e.syncBinding(b)
	}
}

func (e *Engine) syncAllEntities() {
	e.mu.RLock()
	names := make([]string, 0, len(e.defBindings))
	for name := range e.defBindings {
		names = append(names, name)
	}
	e.mu.RUnlock()
	sort.Strings(names)
	for _, name := range names {
		e.syncDefinitionEntities(name)
	}
}

func (e *Engine) removeDefinitionEntities(name string) {
	e.mu.Lock()
	keys := e.defBindings[name]
	delete(e.defBindings, name)
	for _, k := range keys {
		delete(e.bindings, k)
	}
	e.mu.Unlock()
	for _, k := range keys {
		e.deleteEntityKey(k)
	}
}

func (e *Engine) deleteEntityKey(key string) {
	parts := strings.SplitN(key, ".", 3)
	if len(parts) != 3 {
		return
	}
	if err := e.store.Delete(domain.EntityKey{Plugin: parts[0], DeviceID: parts[1], ID: parts[2]}); err != nil {
		slog.Warn("sb-script: delete script entity", "key", key, "err", err)
	}
	// Drop the device once it has no entities left.
	e.mu.RLock()
	for k := range e.bindings {
		if strings.HasPrefix(k, parts[0]+"."+parts[1]+".") {
			e.mu.RUnlock()
			return
		}
	}
	e.mu.RUnlock()
	_ = e.store.Delete(domain.DeviceKey{Plugin: parts[0], ID: parts[1]})
}

func scopeHash(name, scope string) string { return scriptstore.HashInstance(name, scope) }

func instanceKey(hash string) scriptstore.InstanceKey { return scriptstore.InstanceKey{Hash: hash} }

// Instance lifecycle hooks, called by the engine.

func (e *Engine) instanceStarted(name, scope string, startedAt time.Time) {
	kind := "script"
	if scope == "" && e.isAutomation(name) {
		kind = "automation"
	}
	b, ok := e.bindingFor(kind, name, scope)
	if !ok {
		return
	}
	power := true
	if kind == "automation" {
		power = e.automationEnabled(name)
	}
	e.saveBinding(b, power, scriptMeta{Running: true, StartedAt: timePtr(startedAt)})
}

func (e *Engine) instanceStopped(name, scope string) {
	kind := "script"
	if scope == "" && e.isAutomation(name) {
		kind = "automation"
	}
	b, ok := e.bindingFor(kind, name, scope)
	if !ok {
		return
	}
	power := false
	if kind == "automation" {
		// A hard stop doesn't disable an automation; only its switch does.
		power = e.automationEnabled(name)
	}
	e.saveBinding(b, power, scriptMeta{Running: false})
}

func (e *Engine) instanceFailed(name, scope, msg string) {
	kind := "script"
	if scope == "" && e.isAutomation(name) {
		kind = "automation"
	}
	b, ok := e.bindingFor(kind, name, scope)
	if !ok {
		return
	}
	power, meta, _ := e.loadEntity(b.key())
	meta.LastError = msg
	e.saveBinding(b, power, meta)
}

func (e *Engine) isAutomation(name string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.defTypes[name] == "automation"
}

// Commands. They run one at a time, in order, off the messenger goroutine:
// starting a script runs its source.

func (e *Engine) startCommandReceiver() error {
	e.cmdQueue = make(chan func(), 64)
	e.cmdDone = make(chan struct{})
	queue, done := e.cmdQueue, e.cmdDone
	go func() {
		for {
			select {
			case fn := <-queue:
				fn()
			case <-done:
				return
			}
		}
	}()
	cmds := messenger.NewCommands(e.msg, domain.LookupCommand)
	sub, err := cmds.ReceiveMessage(entityPlugin+".>", func(addr messenger.Address, cmd any, m *messenger.Message) {
		traceID := messenger.TraceID(m.Headers)
		select {
		case queue <- func() { e.handleCommand(addr, cmd, traceID) }:
		case <-done:
		}
	})
	if err != nil {
		return err
	}
	e.cmdSub = sub
	return e.msg.Flush()
}

func (e *Engine) stopCommandReceiver() {
	if e.cmdSub != nil {
		e.cmdSub.Unsubscribe()
		e.cmdSub = nil
	}
	if e.cmdDone != nil {
		close(e.cmdDone)
		e.cmdDone = nil
	}
}

func (e *Engine) handleCommand(addr messenger.Address, cmd any, traceID string) {
	e.mu.RLock()
	b, ok := e.bindings[addr.Key()]
	e.mu.RUnlock()
	if !ok {
		e.appendLog("script.entity.unknown", "warn", "command for unknown script entity", traceID, map[string]any{"key": addr.Key()})
		return
	}
	var on bool
	switch cmd.(type) {
	case domain.SwitchTurnOn:
		on = true
	case domain.SwitchTurnOff:
		on = false
	case domain.SwitchToggle:
		power, _, _ := e.loadEntity(b.key())
		on = !power
	default:
		e.appendLog("script.entity.unsupported", "warn", "unsupported command for script entity", traceID, map[string]any{"key": addr.Key(), "command": fmt.Sprintf("%T", cmd)})
		return
	}
	if err := e.applySwitch(b, on, traceID); err != nil {
		slog.Warn("sb-script: script switch", "key", addr.Key(), "on", on, "err", err)
		e.appendLog("script.entity.failed", "error", "script switch command failed", traceID, map[string]any{"key": addr.Key(), "on": on, "error": err.Error()})
	}
}

func (e *Engine) applySwitch(b binding, on bool, traceID string) error {
	if b.kind == "automation" {
		_, meta, _ := e.loadEntity(b.key())
		e.saveBinding(b, on, meta)
		if on {
			_, err := e.StartScriptWithTrace(b.name, "", traceID)
			return err
		}
		return e.StopScriptWithTrace(b.name, "", traceID)
	}
	if on {
		_, err := e.StartScriptWithTrace(b.name, b.scope, traceID)
		return err
	}
	return e.StopScriptGracefully(b.name, b.scope, traceID)
}

// StopScriptGracefully stops an instance, first running its Script stop option
// (if any) with the instance's targets. The instance is gone at once (listed as
// stopped, its switch off, a new start makes a fresh one); its VM stays only to
// run stop and the timers stop sets, for at most gracefulMaxWait.
func (e *Engine) StopScriptGracefully(name, queryRef, traceID string) error {
	hash := scopeHash(name, queryRef)
	e.mu.Lock()
	inst, ok := e.instances[hash]
	if ok && (inst.rt == nil || inst.rt.stopFn == nil) {
		e.mu.Unlock()
		return e.StopScriptWithTrace(name, queryRef, traceID)
	}
	if ok {
		delete(e.instances, hash)
	}
	e.mu.Unlock()
	if !ok {
		e.instanceStopped(name, queryRef)
		return nil
	}
	e.store.Delete(instanceKey(hash))
	e.instanceStopped(name, queryRef)
	e.appendLog("script.stop.graceful", "info", "script graceful stop requested", traceID, map[string]any{
		"name": name, "query_ref": queryRef, "instance_hash": hash,
	})

	rt, vm := inst.rt, inst.vm
	e.addFading(hash, vm, inst.record)
	vm.timers.cancelAll() // the script's own pending work ends here
	for _, sub := range vm.subs {
		sub.Unsubscribe()
	}
	ran := make(chan struct{})
	vm.enqueue(func() {
		defer close(ran)
		targets := rt.resolveTargets(rt.spec.targets)
		ctx := rt.newContext(targets, nil, traceID)
		if err := vm.L.CallByParam(lua.P{Fn: rt.stopFn, NRet: 0, Protect: true}, ctx); err != nil {
			slog.Warn("sb-script: Script stop error", "name", name, "err", err)
			rt.log("script.stop.failed", "error", "script stop callback failed", traceID, map[string]any{"error": err.Error()})
		}
	})
	go func() {
		defer e.endFading(hash, vm)
		deadline := time.Now().Add(gracefulMaxWait)
		select {
		case <-ran:
		case <-vm.done:
			return
		case <-time.After(gracefulMaxWait):
			return
		}
		// Wait for the timers stop set (fades, follow-up steps) to finish. Idle
		// twice in a row, so a callback that is mid-run can schedule its next step.
		idle := 0
		for idle < 2 && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			if vm.timers.pending() == 0 && len(vm.queue) == 0 {
				idle++
			} else {
				idle = 0
			}
		}
	}()
	return nil
}

// A graceful stop leaves the VM running its stop option for a while. Until it
// finishes it still counts for hard stops: stopping the same instance, a
// stopAll overlapping its targets, or starting the instance again ends it, so
// a fade can't turn lights back on after a wall switch turned them off.
type fadingVM struct {
	vm      *luaVM
	name    string
	scope   string
	targets []string
}

func (e *Engine) addFading(hash string, vm *luaVM, rec scriptstore.Instance) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fading == nil {
		e.fading = make(map[string]fadingVM)
	}
	if prev, ok := e.fading[hash]; ok && prev.vm != vm {
		go prev.vm.close()
	}
	e.fading[hash] = fadingVM{vm: vm, name: rec.Name, scope: rec.QueryRef, targets: rec.ResolvedTargets}
}

func (e *Engine) endFading(hash string, vm *luaVM) {
	e.mu.Lock()
	if cur, ok := e.fading[hash]; ok && cur.vm == vm {
		delete(e.fading, hash)
	}
	e.mu.Unlock()
	vm.close()
}

// cutFading ends the fading VM for hash, if any.
func (e *Engine) cutFading(hash string) {
	e.mu.Lock()
	f, ok := e.fading[hash]
	delete(e.fading, hash)
	e.mu.Unlock()
	if ok {
		go f.vm.close()
	}
}

// cutFadingOverlapping ends fading VMs whose targets overlap targets.
func (e *Engine) cutFadingOverlapping(targets map[string]struct{}) {
	e.mu.Lock()
	var cut []*luaVM
	for hash, f := range e.fading {
		for _, k := range f.targets {
			if _, ok := targets[k]; ok {
				cut = append(cut, f.vm)
				delete(e.fading, hash)
				break
			}
		}
	}
	e.mu.Unlock()
	for _, vm := range cut {
		go vm.close()
	}
}
