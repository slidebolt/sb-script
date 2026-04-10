package engine

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"

	domain "github.com/slidebolt/sb-domain"
	logging "github.com/slidebolt/sb-logging-sdk"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	scriptstore "github.com/slidebolt/sb-script/internal/store"
	storage "github.com/slidebolt/sb-storage-sdk"
	lua "github.com/yuin/gopher-lua"
)

// Engine manages passive automation definitions and active automation VMs.
type Engine struct {
	msg    messenger.Messenger
	store  storage.Storage
	logger logging.Store

	mu           sync.RWMutex
	definitions  map[string]string             // name -> lua source
	defTypes     map[string]string             // name -> definition type
	instances    map[string]*persistedInstance // hash(name+query) -> active automation VM
	defWatcher   *storage.Watcher
	defDeleteSub messenger.Subscription
}

type persistedInstance struct {
	vm     *luaVM
	record scriptstore.Instance
}

type watcherSubscription struct {
	watcher *storage.Watcher
}

func (w watcherSubscription) Unsubscribe() error {
	if w.watcher != nil {
		w.watcher.Stop()
	}
	return nil
}

func New(msg messenger.Messenger, store storage.Storage) (*Engine, error) {
	return NewWithLogger(msg, store, nil)
}

func NewWithLogger(msg messenger.Messenger, store storage.Storage, logger logging.Store) (*Engine, error) {
	e := &Engine{
		msg:         msg,
		store:       store,
		logger:      logger,
		definitions: make(map[string]string),
		defTypes:    make(map[string]string),
		instances:   make(map[string]*persistedInstance),
	}

	if err := e.ensureLayout(); err != nil {
		return nil, err
	}

	entries, err := store.Search("sb-script.scripts.*")
	if err != nil {
		return nil, err
	}
	var automationNames []string
	for _, entry := range entries {
		var def scriptstore.Definition
		if err := json.Unmarshal(entry.Data, &def); err != nil {
			slog.Warn("sb-script: skip bad definition", "key", entry.Key, "err", err)
			continue
		}
		if def.Name == "" {
			continue
		}
		e.definitions[def.Name] = def.Source
		e.defTypes[def.Name] = def.Type
		if def.Type == "automation" {
			automationNames = append(automationNames, def.Name)
		}
	}

	instEntries, err := store.Search("sb-script.instances.>")
	if err != nil {
		return nil, err
	}
	for _, entry := range instEntries {
		var inst scriptstore.Instance
		if err := json.Unmarshal(entry.Data, &inst); err != nil {
			slog.Warn("sb-script: skip bad instance", "key", entry.Key, "err", err)
			continue
		}
		if err := e.startInstance(inst.Name, inst.QueryRef); err != nil {
			slog.Warn("sb-script: resume instance error", "name", inst.Name, "queryRef", inst.QueryRef, "err", err)
		}
	}

	// Auto-start automation definitions that are not already running from a persisted instance.
	for _, name := range automationNames {
		hash := scriptstore.HashInstance(name, "")
		e.mu.RLock()
		_, running := e.instances[hash]
		e.mu.RUnlock()
		if !running {
			if err := e.startInstance(name, ""); err != nil {
				slog.Warn("sb-script: auto-start automation error", "name", name, "err", err)
			}
		}
	}

	if err := e.startDefinitionWatcher(entries); err != nil {
		return nil, err
	}
	if err := e.startDefinitionDeleteWatcher(); err != nil {
		e.Shutdown()
		return nil, err
	}

	return e, nil
}

func (e *Engine) DeleteDefinition(name string) error {
	e.removeDefinitionByName(name)
	return e.store.Delete(scriptstore.DefinitionKey{Name: name})
}

func (e *Engine) StartScript(name, queryRef string) (string, error) {
	return e.StartScriptWithTrace(name, queryRef, "")
}

func (e *Engine) StartScriptWithTrace(name, queryRef, traceID string) (string, error) {
	hash := scriptstore.HashInstance(name, queryRef)
	e.mu.RLock()
	_, exists := e.instances[hash]
	e.mu.RUnlock()
	if exists {
		e.appendLog("script.start.skipped", "info", "script start skipped; instance already running", traceID, map[string]any{
			"name":          name,
			"query_ref":     queryRef,
			"instance_hash": hash,
		})
		return hash, nil
	}
	e.appendLog("script.start.requested", "info", "script start requested", traceID, map[string]any{
		"name":          name,
		"query_ref":     queryRef,
		"instance_hash": hash,
	})
	return hash, e.startInstance(name, queryRef)
}

func (e *Engine) StopScript(name, queryRef string) error {
	return e.StopScriptWithTrace(name, queryRef, "")
}

func (e *Engine) StopScriptWithTrace(name, queryRef, traceID string) error {
	hash := scriptstore.HashInstance(name, queryRef)
	var vm *luaVM
	e.mu.Lock()
	if instVM, ok := e.instances[hash]; ok {
		vm = instVM.vm
		delete(e.instances, hash)
	}
	e.mu.Unlock()
	if vm != nil {
		e.store.Delete(scriptstore.InstanceKey{Hash: hash})
		go vm.close()
	}
	e.appendLog("script.stop.requested", "info", "script stop requested", traceID, map[string]any{
		"name":          name,
		"query_ref":     queryRef,
		"instance_hash": hash,
	})
	return nil
}

func (e *Engine) StopAllScripts(queryRef string) error {
	return e.StopAllScriptsWithTrace(queryRef, "")
}

func (e *Engine) StopAllScriptsWithTrace(queryRef, traceID string) error {
	e.appendLog("script.stop_all.requested", "info", "script stop_all requested", traceID, map[string]any{
		"query_ref": queryRef,
	})
	targetEntries, err := e.resolveQueryRefEntries(queryRef)
	if err != nil {
		return err
	}
	if len(targetEntries) == 0 {
		e.appendLog("script.stop_all.completed", "info", "script stop_all completed with no targets", traceID, map[string]any{
			"query_ref": queryRef,
		})
		return nil
	}
	targets := make(map[string]struct{}, len(targetEntries))
	for _, entry := range targetEntries {
		targets[entry.Key] = struct{}{}
	}

	instances, err := e.loadRunningInstances()
	if err != nil {
		return err
	}
	for _, inst := range instances {
		instTargets, err := e.resolveInstanceTargets(inst)
		if err != nil {
			slog.Warn("sb-script: skip script during stop_all target resolution", "name", inst.Name, "hash", inst.Hash, "err", err)
			continue
		}
		if !hasOverlap(targets, instTargets) {
			continue
		}
		e.appendLog("script.stop_all.match", "info", "script stop_all matched running instance", traceID, map[string]any{
			"name":          inst.Name,
			"query_ref":     inst.QueryRef,
			"instance_hash": inst.Hash,
		})
		if err := e.StopScriptWithTrace(inst.Name, inst.QueryRef, traceID); err != nil {
			return fmt.Errorf("stop script %s (%s): %w", inst.Name, inst.Hash, err)
		}
	}
	e.appendLog("script.stop_all.completed", "info", "script stop_all completed", traceID, map[string]any{
		"query_ref": queryRef,
	})
	return nil
}

func (e *Engine) Shutdown() {
	if e.defWatcher != nil {
		e.defWatcher.Stop()
		e.defWatcher = nil
	}
	if e.defDeleteSub != nil {
		e.defDeleteSub.Unsubscribe()
		e.defDeleteSub = nil
	}
	e.mu.Lock()
	toClose := make([]*luaVM, 0, len(e.instances))
	for _, instVM := range e.instances {
		toClose = append(toClose, instVM.vm)
	}
	e.instances = make(map[string]*persistedInstance)
	e.mu.Unlock()
	for _, vm := range toClose {
		vm.close()
	}
}

func (e *Engine) startDefinitionWatcher(entries []storage.Entry) error {
	watcher, err := storage.Watch(e.msg, storage.Query{Pattern: "sb-script.scripts.>"}, storage.WatchHandlers{
		OnAdd: func(_ string, data json.RawMessage) {
			e.reconcileDefinition(data, nil)
		},
		OnUpdate: func(_ string, data json.RawMessage) {
			e.reconcileDefinition(data, nil)
		},
		OnRemove: func(_ string, data json.RawMessage) {
			e.removeDefinition(data)
		},
	})
	if err != nil {
		return err
	}
	for _, entry := range entries {
		watcher.Populate(entry.Key, entry.Data)
	}
	e.defWatcher = watcher
	return nil
}

func (e *Engine) startDefinitionDeleteWatcher() error {
	sub, err := e.msg.Subscribe("storage.delete", func(m *messenger.Message) {
		var req struct {
			Key    string                `json:"key"`
			Target storage.StorageTarget `json:"target,omitempty"`
		}
		if err := json.Unmarshal(m.Data, &req); err != nil {
			return
		}
		if req.Target != "" && req.Target != storage.State {
			return
		}
		const defPrefix = "sb-script.scripts."
		if !strings.HasPrefix(req.Key, defPrefix) {
			return
		}
		name := strings.TrimPrefix(req.Key, defPrefix)
		if name == "" {
			return
		}
		e.removeDefinitionByName(name)
	})
	if err != nil {
		return err
	}
	if err := e.msg.Flush(); err != nil {
		sub.Unsubscribe()
		return err
	}
	e.defDeleteSub = sub
	return nil
}

func (e *Engine) ensureLayout() error {
	if err := e.store.Save(scriptstore.Root{
		ID:   "sb-script",
		Type: "service",
		Name: "sb-script",
	}); err != nil {
		return err
	}
	return e.store.Save(scriptstore.Scripts{
		ID:   "scripts",
		Type: "script-collection",
		Name: "Scripts",
	})
}

func (e *Engine) startInstance(name, queryRef string) error {
	source, ok := e.definitionSource(name)
	if !ok || source == "" {
		return &ErrNotFound{Name: name}
	}

	var queryOverride *storage.Query
	if queryRef != "" {
		resolved, err := storage.ResolveQueryRef(e.store, queryRef)
		if err != nil {
			return fmt.Errorf("resolve query ref %s: %w", queryRef, err)
		}
		queryOverride = &resolved
	}

	vm := newLuaVM()
	vm.injectServices(e.msg, e.store, e)

	rt := &activationRuntime{
		engine:        e,
		msg:           e.msg,
		store:         e.store,
		vm:            vm,
		name:          name,
		queryRef:      queryRef,
		queryOverride: queryOverride,
		random:        rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	rt.injectAutomationAPI()

	var execErr error
	vm.exec(func() {
		if err := vm.L.DoString(source); err != nil {
			execErr = err
		}
	})
	if execErr != nil {
		vm.close()
		return execErr
	}
	if !rt.activated {
		vm.close()
		return &ErrNotFound{Name: name}
	}

	// For Script() definitions (no trigger), invoke the function once immediately.
	if rt.spec.trigger.kind == "" && rt.scriptFn != nil {
		fn := rt.scriptFn
		rt.initialInvocations = append(rt.initialInvocations, func() {
			traceID := fmt.Sprintf("sb-script-trigger-%d", time.Now().UTC().UnixNano())
			targets := rt.resolveTargets(rt.spec.targets)
			rt.markFired(targets)
			rt.log("script.invoke.started", "info", "script invoke started", traceID, nil)
			ctx := rt.newContext(targets, nil, traceID)
			if err := vm.L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, ctx); err != nil {
				rt.markError(err)
				rt.log("script.invoke.failed", "error", "script invoke failed", traceID, map[string]any{"error": err.Error()})
				slog.Warn("sb-script: Script callback error", "name", name, "err", err)
				return
			}
			rt.log("script.invoke.completed", "info", "script invoke completed", traceID, nil)
		})
	}

	hash := scriptstore.HashInstance(name, queryRef)
	record := scriptstore.Instance{
		Name:      name,
		QueryRef:  queryRef,
		Hash:      hash,
		Status:    "running",
		Trigger:   triggerInfo(rt.spec.trigger),
		Targets:   targetInfo(rt.spec.targets),
		StartedAt: timePtr(time.Now()),
	}
	if rt.nextFireAt != nil {
		record.NextFireAt = timePtr(*rt.nextFireAt)
	}
	e.mu.Lock()
	e.instances[hash] = &persistedInstance{vm: vm, record: record}
	e.mu.Unlock()

	// Enqueue initial invocations AFTER the instance is registered so that
	// markFired can find the record when these run on the VM goroutine.
	for _, fn := range rt.initialInvocations {
		vm.enqueue(fn)
	}

	return e.store.Save(record)
}

func (e *Engine) definitionSource(name string) (string, bool) {
	data, err := e.store.Get(scriptstore.DefinitionKey{Name: name})
	if err == nil {
		var def scriptstore.Definition
		if json.Unmarshal(data, &def) == nil && def.Name != "" && def.Source != "" {
			e.mu.Lock()
			e.definitions[name] = def.Source
			e.defTypes[name] = def.Type
			e.mu.Unlock()
			return def.Source, true
		}
	}
	entries, err := e.store.Search(scriptstore.DefinitionKey{Name: name}.Key())
	if err == nil && len(entries) > 0 {
		var def scriptstore.Definition
		if json.Unmarshal(entries[0].Data, &def) == nil && def.Name != "" && def.Source != "" {
			e.mu.Lock()
			e.definitions[name] = def.Source
			e.defTypes[name] = def.Type
			e.mu.Unlock()
			return def.Source, true
		}
	}

	e.mu.RLock()
	source, ok := e.definitions[name]
	e.mu.RUnlock()
	return source, ok && source != ""
}

func (e *Engine) reconcileDefinition(data json.RawMessage, _ *scriptstore.Definition) {
	var def scriptstore.Definition
	if err := json.Unmarshal(data, &def); err != nil {
		return
	}
	if def.Name == "" {
		return
	}

	e.mu.Lock()
	prevSource, hadPrev := e.definitions[def.Name]
	prevType := e.defTypes[def.Name]
	e.definitions[def.Name] = def.Source
	e.defTypes[def.Name] = def.Type
	e.mu.Unlock()

	if hadPrev && prevSource == def.Source && prevType == def.Type {
		return
	}

	runningRefs := e.stopInstancesForDefinition(def.Name)
	switch def.Type {
	case "automation":
		if len(runningRefs) == 0 {
			runningRefs = []string{""}
		} else if !containsString(runningRefs, "") {
			runningRefs = append([]string{""}, runningRefs...)
		}
	case "script":
		if len(runningRefs) == 0 {
			return
		}
	default:
		if len(runningRefs) == 0 {
			return
		}
	}

	for _, queryRef := range dedupeStrings(runningRefs) {
		if err := e.startInstance(def.Name, queryRef); err != nil {
			slog.Warn("sb-script: hot-reload start error", "name", def.Name, "queryRef", queryRef, "err", err)
		}
	}
}

func (e *Engine) removeDefinition(data json.RawMessage) {
	var def scriptstore.Definition
	if err := json.Unmarshal(data, &def); err != nil {
		return
	}
	if def.Name == "" {
		return
	}
	e.removeDefinitionByName(def.Name)
}

func (e *Engine) removeDefinitionByName(name string) {
	e.mu.Lock()
	delete(e.definitions, name)
	delete(e.defTypes, name)
	e.mu.Unlock()
	e.stopInstancesForDefinition(name)
}

func (e *Engine) stopInstancesForDefinition(name string) []string {
	e.mu.Lock()
	type toStop struct {
		hash string
		vm   *luaVM
	}
	var (
		stops     []toStop
		queryRefs []string
	)
	for hash, instVM := range e.instances {
		if instVM.record.Name != name {
			continue
		}
		stops = append(stops, toStop{hash: hash, vm: instVM.vm})
		queryRefs = append(queryRefs, instVM.record.QueryRef)
		delete(e.instances, hash)
	}
	e.mu.Unlock()

	for _, stop := range stops {
		e.store.Delete(scriptstore.InstanceKey{Hash: stop.hash})
		stop.vm.close()
	}
	return queryRefs
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func dedupeStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func (e *Engine) instanceRecord(hash string) *scriptstore.Instance {
	e.mu.RLock()
	if instVM, ok := e.instances[hash]; ok {
		cp := instVM.record
		e.mu.RUnlock()
		return &cp
	}
	e.mu.RUnlock()
	data, err := e.store.Get(scriptstore.InstanceKey{Hash: hash})
	if err != nil {
		return nil
	}
	var inst scriptstore.Instance
	if err := json.Unmarshal(data, &inst); err != nil {
		return nil
	}
	return &inst
}

func (e *Engine) loadRunningInstances() ([]scriptstore.Instance, error) {
	entries, err := e.store.Search("sb-script.instances.>")
	if err != nil {
		return nil, fmt.Errorf("search script instances: %w", err)
	}
	out := make([]scriptstore.Instance, 0, len(entries))
	for _, entry := range entries {
		var inst scriptstore.Instance
		if err := json.Unmarshal(entry.Data, &inst); err != nil {
			continue
		}
		out = append(out, inst)
	}
	return out, nil
}

func (e *Engine) resolveInstanceTargets(inst scriptstore.Instance) (map[string]struct{}, error) {
	if len(inst.ResolvedTargets) > 0 {
		out := make(map[string]struct{}, len(inst.ResolvedTargets))
		for _, key := range inst.ResolvedTargets {
			out[key] = struct{}{}
		}
		return out, nil
	}
	queryRef := inst.Targets.QueryRef
	if queryRef == "" {
		queryRef = inst.QueryRef
	}
	entries, err := e.resolveQueryRefEntries(queryRef)
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		out[entry.Key] = struct{}{}
	}
	return out, nil
}

func (e *Engine) resolveQueryRefEntries(queryRef string) ([]storage.Entry, error) {
	if queryRef == "" {
		return nil, nil
	}
	q, err := storage.ResolveQueryRef(e.store, queryRef)
	if err != nil {
		return nil, fmt.Errorf("resolve query ref %s: %w", queryRef, err)
	}
	return e.store.Query(q)
}

func hasOverlap(a, b map[string]struct{}) bool {
	for key := range b {
		if _, ok := a[key]; ok {
			return true
		}
	}
	return false
}

func (e *Engine) saveInstanceRecord(hash string, update func(*scriptstore.Instance)) {
	e.mu.Lock()
	instVM, ok := e.instances[hash]
	if !ok {
		e.mu.Unlock()
		return
	}
	update(&instVM.record)
	record := instVM.record
	e.mu.Unlock()
	if err := e.store.Save(record); err != nil {
		slog.Warn("sb-script: save instance metadata error", "hash", hash, "err", err)
	}
}

type automationSpec struct {
	trigger triggerSpec
	targets targetSpec
}

type triggerSpec struct {
	kind     string
	key      string
	queryRef string
	query    storage.Query
	min      time.Duration
	max      time.Duration
}

type targetSpec struct {
	kind     string
	key      string
	queryRef string
	query    storage.Query
}

type activationRuntime struct {
	engine             *Engine
	msg                messenger.Messenger
	store              storage.Storage
	vm                 *luaVM
	name               string
	queryRef           string
	queryOverride      *storage.Query
	activated          bool
	random             *rand.Rand
	spec               automationSpec
	scriptFn           *lua.LFunction
	nextFireAt         *time.Time
	initialInvocations []func() // enqueued after instance registration
}

func (rt *activationRuntime) injectAutomationAPI() {
	L := rt.vm.L

	L.SetGlobal("Entity", L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(1)
		tbl := L.NewTable()
		L.SetField(tbl, "kind", lua.LString("entity"))
		L.SetField(tbl, "key", lua.LString(key))
		L.Push(tbl)
		return 1
	}))

	L.SetGlobal("Query", L.NewFunction(func(L *lua.LState) int {
		query, err := queryFromLuaValue(L.Get(1))
		if err != nil {
			L.RaiseError("query expects string pattern or query table: %v", err)
			return 0
		}
		tbl := L.NewTable()
		L.SetField(tbl, "kind", lua.LString("query"))
		L.SetField(tbl, "query", anyToLua(L, queryToMap(query)))
		L.Push(tbl)
		return 1
	}))

	L.SetGlobal("QueryRef", L.NewFunction(func(L *lua.LState) int {
		ref := L.CheckString(1)
		query, err := storage.ResolveQueryRef(rt.store, ref)
		if err != nil {
			L.RaiseError("query ref %q: %v", ref, err)
			return 0
		}
		tbl := L.NewTable()
		L.SetField(tbl, "kind", lua.LString("query_ref"))
		L.SetField(tbl, "queryRef", lua.LString(ref))
		L.SetField(tbl, "query", anyToLua(L, queryToMap(query)))
		L.Push(tbl)
		return 1
	}))

	L.SetGlobal("None", L.NewFunction(func(L *lua.LState) int {
		tbl := L.NewTable()
		L.SetField(tbl, "kind", lua.LString("none"))
		L.Push(tbl)
		return 1
	}))

	L.SetGlobal("Interval", L.NewFunction(func(L *lua.LState) int {
		tbl := L.NewTable()
		L.SetField(tbl, "kind", lua.LString("interval"))
		switch v := L.Get(1).(type) {
		case lua.LNumber:
			L.SetField(tbl, "min", v)
			L.SetField(tbl, "max", v)
		case *lua.LTable:
			L.SetField(tbl, "min", L.GetField(v, "min"))
			L.SetField(tbl, "max", L.GetField(v, "max"))
		default:
			L.ArgError(1, "interval expects number or table")
		}
		L.Push(tbl)
		return 1
	}))

	L.SetGlobal("Automation", L.NewFunction(func(L *lua.LState) int {
		name := L.CheckString(1)
		specTbl := L.CheckTable(2)
		fn := L.CheckFunction(3)
		if name != rt.name {
			return 0
		}
		spec := parseAutomationSpec(L, specTbl, rt.queryRef, rt.queryOverride)
		rt.spec = spec
		rt.activate(spec, fn)
		rt.activated = true
		return 0
	}))

	// Script registers a one-shot script: the function is called once when the
	// instance is started and may use ctx.after for deferred work. Unlike
	// Automation, Script has no trigger and is only started explicitly via
	// ctx.scripts:start. Definitions with type="automation" use Automation;
	// one-shot effect scripts use Script.
	L.SetGlobal("Script", L.NewFunction(func(L *lua.LState) int {
		name := L.CheckString(1)
		fn := L.CheckFunction(2)
		if name != rt.name {
			return 0
		}
		spec := automationSpec{targets: targetSpec{kind: "none"}}
		if rt.queryOverride != nil {
			spec.targets = targetSpec{kind: "query_ref", queryRef: rt.queryRef, query: *rt.queryOverride}
		}
		rt.spec = spec
		rt.scriptFn = fn
		rt.activated = true
		return 0
	}))
}

func parseAutomationSpec(L *lua.LState, specTbl *lua.LTable, queryRef string, queryOverride *storage.Query) automationSpec {
	spec := automationSpec{
		targets: targetSpec{kind: "none"},
	}
	if trg, ok := L.GetField(specTbl, "trigger").(*lua.LTable); ok {
		spec.trigger = parseTriggerSpec(L, trg)
	}
	if queryOverride != nil {
		spec.targets = targetSpec{kind: "query_ref", queryRef: queryRef, query: *queryOverride}
	} else if tgt, ok := L.GetField(specTbl, "targets").(*lua.LTable); ok {
		spec.targets = parseTargetSpec(L, tgt)
	}
	return spec
}

func parseTriggerSpec(L *lua.LState, tbl *lua.LTable) triggerSpec {
	spec := triggerSpec{kind: lua.LVAsString(L.GetField(tbl, "kind"))}
	spec.key = lua.LVAsString(L.GetField(tbl, "key"))
	spec.queryRef = lua.LVAsString(L.GetField(tbl, "queryRef"))
	if qtbl, ok := L.GetField(tbl, "query").(*lua.LTable); ok {
		query, err := queryFromLuaTable(qtbl)
		if err == nil {
			spec.query = query
		}
	}
	if spec.kind == "interval" {
		spec.min = durationFromSecs(float64(lua.LVAsNumber(L.GetField(tbl, "min"))))
		spec.max = durationFromSecs(float64(lua.LVAsNumber(L.GetField(tbl, "max"))))
		if spec.max < spec.min {
			spec.max = spec.min
		}
	}
	return spec
}

func parseTargetSpec(L *lua.LState, tbl *lua.LTable) targetSpec {
	spec := targetSpec{
		kind:     lua.LVAsString(L.GetField(tbl, "kind")),
		key:      lua.LVAsString(L.GetField(tbl, "key")),
		queryRef: lua.LVAsString(L.GetField(tbl, "queryRef")),
	}
	if qtbl, ok := L.GetField(tbl, "query").(*lua.LTable); ok {
		query, err := queryFromLuaTable(qtbl)
		if err == nil {
			spec.query = query
		}
	}
	return spec
}

func (rt *activationRuntime) activate(spec automationSpec, fn *lua.LFunction) {
	switch spec.trigger.kind {
	case "entity":
		subject := "state.changed." + spec.trigger.key
		sub, err := rt.msg.Subscribe(subject, func(m *messenger.Message) {
			var ent domain.Entity
			if err := json.Unmarshal(m.Data, &ent); err != nil {
				return
			}
			traceID := messenger.TraceID(m.Headers)
			if traceID == "" {
				traceID = fmt.Sprintf("sb-script-trigger-%d", time.Now().UTC().UnixNano())
			}
			rt.log("automation.triggered", "info", "automation entity trigger received", traceID, map[string]any{
				"trigger_kind": spec.trigger.kind,
				"trigger_key":  spec.trigger.key,
				"entity_key":   ent.Key(),
			})
			rt.invoke(fn, spec, &ent, traceID)
		})
		if err == nil {
			rt.vm.subs = append(rt.vm.subs, sub)
		}
	case "query", "query_ref":
		invokeEntity := func(_ string, data json.RawMessage) {
			var ent domain.Entity
			if err := json.Unmarshal(data, &ent); err != nil {
				return
			}
			traceID := fmt.Sprintf("sb-script-trigger-%d", time.Now().UTC().UnixNano())
			rt.log("automation.triggered", "info", "automation query trigger received", traceID, map[string]any{
				"trigger_kind": spec.trigger.kind,
				"entity_key":   ent.Key(),
			})
			rt.invoke(fn, spec, &ent, traceID)
		}
		w, err := storage.Watch(rt.msg, spec.trigger.query, storage.WatchHandlers{
			OnAdd:    invokeEntity,
			OnUpdate: invokeEntity,
		})
		if err == nil {
			rt.vm.subs = append(rt.vm.subs, watcherSubscription{watcher: w})
			// Fire for entities already in storage that match the trigger query.
			// Watch only sees state.changed events going forward; a pre-existing
			// entity that doesn't change state would never trigger otherwise.
			// Enqueue so invocations run after startInstance registers the instance
			// record, which is required for markFired to persist the FireCount.
			if entries, qerr := rt.store.Query(spec.trigger.query); qerr == nil {
				for _, entry := range entries {
					data := entry.Data
					// Defer to after instance registration so markFired can find the record.
					rt.initialInvocations = append(rt.initialInvocations, func() {
						invokeEntity("", data)
					})
				}
			}
		}
	case "interval":
		rt.scheduleEvery(spec.trigger, func() {
			traceID := fmt.Sprintf("sb-script-trigger-%d", time.Now().UTC().UnixNano())
			rt.log("automation.triggered", "info", "automation interval trigger fired", traceID, map[string]any{
				"trigger_kind": spec.trigger.kind,
			})
			rt.invoke(fn, spec, nil, traceID)
		})
	}
}

func (rt *activationRuntime) invoke(fn *lua.LFunction, spec automationSpec, trigger *domain.Entity, traceID string) {
	targets := rt.resolveTargets(spec.targets)
	rt.markFired(targets)
	targetKeys := make([]string, 0, len(targets))
	for _, target := range targets {
		targetKeys = append(targetKeys, target.Key())
	}
	rt.log("automation.targets.resolved", "info", "automation targets resolved", traceID, map[string]any{
		"target_kind":    spec.targets.kind,
		"resolved_count": len(targets),
		"resolved":       targetKeys,
	})
	rt.vm.enqueue(func() {
		rt.log("automation.invoke.started", "info", "automation invoke started", traceID, nil)
		ctx := rt.newContext(targets, trigger, traceID)
		if err := rt.vm.L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, ctx); err != nil {
			rt.markError(err)
			rt.log("automation.invoke.failed", "error", "automation invoke failed", traceID, map[string]any{"error": err.Error()})
			slog.Warn("sb-script: Automation callback error", "name", rt.name, "err", err)
			return
		}
		rt.log("automation.invoke.completed", "info", "automation invoke completed", traceID, nil)
	})
}

func (rt *activationRuntime) resolveTargets(spec targetSpec) []domain.Entity {
	switch spec.kind {
	case "entity":
		targets, err := resolveQuery(rt.store, storage.Query{Pattern: spec.key})
		if err != nil {
			slog.Warn("sb-script: resolve targets error", "name", rt.name, "key", spec.key, "err", err)
			return nil
		}
		return targets
	case "query":
		targets, err := resolveQuery(rt.store, spec.query)
		if err != nil {
			slog.Warn("sb-script: resolve targets error", "name", rt.name, "query", queryIdentity(spec.query), "err", err)
			return nil
		}
		return targets
	case "query_ref":
		targets, err := resolveQuery(rt.store, spec.query)
		if err != nil {
			slog.Warn("sb-script: resolve targets error", "name", rt.name, "queryRef", spec.queryRef, "err", err)
			return nil
		}
		return targets
	default:
		return nil
	}
}

func (rt *activationRuntime) newContext(targets []domain.Entity, trigger *domain.Entity, traceID string) *lua.LTable {
	L := rt.vm.L
	ctx := L.NewTable()
	L.SetField(ctx, "targets", entitiesToTable(L, targets))

	triggerTbl := L.NewTable()
	if trigger != nil {
		L.SetField(triggerTbl, "entity", entityToTable(L, *trigger))
	}
	L.SetField(ctx, "trigger", triggerTbl)

	L.SetField(ctx, "query", L.NewFunction(func(L *lua.LState) int {
		query, err := queryFromLuaValue(L.Get(1))
		if err != nil {
			L.Push(L.NewTable())
			return 1
		}
		entities, err := resolveQuery(rt.store, query)
		if err != nil {
			L.Push(L.NewTable())
			return 1
		}
		L.Push(entitiesToTable(L, entities))
		return 1
	}))
	L.SetField(ctx, "queryOne", L.NewFunction(func(L *lua.LState) int {
		query, err := queryFromLuaValue(L.Get(1))
		if err != nil {
			L.Push(lua.LNil)
			return 1
		}
		entities, err := resolveQuery(rt.store, query)
		if err != nil || len(entities) == 0 {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(entityToTable(L, entities[0]))
		return 1
	}))
	L.SetField(ctx, "decision", L.NewFunction(func(L *lua.LState) int {
		label := L.CheckString(1)
		data := map[string]any{
			"label": label,
		}
		if L.GetTop() >= 2 {
			if tbl, ok := L.Get(2).(*lua.LTable); ok {
				for k, v := range luaTableToMap(tbl) {
					data[k] = v
				}
			}
		}
		rt.log("automation.decision", "info", "automation decision recorded", traceID, data)
		return 0
	}))
	L.SetField(ctx, "send", L.NewFunction(func(L *lua.LState) int {
		entityTbl := L.CheckTable(1)
		action := L.CheckString(2)
		paramsJSON := []byte("{}")
		if L.GetTop() >= 3 {
			if paramsTbl, ok := L.Get(3).(*lua.LTable); ok {
				paramsJSON, _ = json.Marshal(luaTableToMap(paramsTbl))
			}
		}
		if _, ok := domain.LookupCommand(action); !ok {
			slog.Warn("sb-script: ctx.send unknown action, not published", "name", rt.name, "action", action)
			rt.log("automation.command.rejected", "warn", "ctx.send action rejected", traceID, map[string]any{
				"action": action,
			})
			return 0
		}
		key := lua.LVAsString(L.GetField(entityTbl, "key"))
		rt.log("automation.command.published", "info", "ctx.send published command", traceID, map[string]any{
			"recipient": key,
			"action":    action,
		})
		headers := messenger.WithOrigin(messenger.WithTraceID(nil, traceID), "sb-script", key, action)
		_ = rt.msg.PublishWithHeaders(key+".command."+action, paramsJSON, headers)
		return 0
	}))
	L.SetField(ctx, "after", L.NewFunction(func(L *lua.LState) int {
		secs := float64(L.CheckNumber(1))
		cb := L.CheckFunction(2)
		id := rt.scheduleAfter(durationFromSecs(secs), func() {
			child := rt.newContext(targets, trigger, traceID)
			if err := rt.vm.L.CallByParam(lua.P{Fn: cb, NRet: 0, Protect: true}, child); err != nil {
				slog.Warn("sb-script: ctx.after callback error", "name", rt.name, "err", err)
			}
		})
		L.Push(lua.LNumber(id))
		return 1
	}))
	L.SetField(ctx, "every", L.NewFunction(func(L *lua.LState) int {
		secs := float64(L.CheckNumber(1))
		cb := L.CheckFunction(2)
		id := rt.scheduleEvery(triggerSpec{min: durationFromSecs(secs), max: durationFromSecs(secs)}, func() {
			child := rt.newContext(targets, trigger, traceID)
			if err := rt.vm.L.CallByParam(lua.P{Fn: cb, NRet: 0, Protect: true}, child); err != nil {
				slog.Warn("sb-script: ctx.every callback error", "name", rt.name, "err", err)
			}
		})
		L.Push(lua.LNumber(id))
		return 1
	}))
	L.SetField(ctx, "cancel", L.NewFunction(func(L *lua.LState) int {
		id := int64(L.CheckNumber(1))
		rt.vm.timers.cancel(id)
		return 0
	}))

	scriptsTbl := L.NewTable()
	L.SetField(scriptsTbl, "start", L.NewFunction(func(L *lua.LState) int {
		nameIndex, queryIndex := scriptControlArgIndexes(L)
		name := L.CheckString(nameIndex)
		queryRef := scriptControlQueryRef(L.Get(queryIndex))
		rt.log("script.control.start", "info", "ctx.scripts:start invoked", traceID, map[string]any{"target_name": name, "target_query_ref": queryRef})
		if _, err := rt.engine.StartScriptWithTrace(name, queryRef, traceID); err != nil {
			L.RaiseError("start script %q: %v", name, err)
			return 0
		}
		return 0
	}))
	L.SetField(scriptsTbl, "stop", L.NewFunction(func(L *lua.LState) int {
		nameIndex, queryIndex := scriptControlArgIndexes(L)
		name := L.CheckString(nameIndex)
		queryRef := scriptControlQueryRef(L.Get(queryIndex))
		rt.log("script.control.stop", "info", "ctx.scripts:stop invoked", traceID, map[string]any{"target_name": name, "target_query_ref": queryRef})
		if err := rt.engine.StopScriptWithTrace(name, queryRef, traceID); err != nil {
			L.RaiseError("stop script %q: %v", name, err)
			return 0
		}
		return 0
	}))
	L.SetField(scriptsTbl, "stopAll", L.NewFunction(func(L *lua.LState) int {
		queryRef := scriptControlQueryRef(L.Get(1))
		rt.log("script.control.stop_all", "info", "ctx.scripts:stopAll invoked", traceID, map[string]any{"target_query_ref": queryRef})
		if err := rt.engine.StopAllScriptsWithTrace(queryRef, traceID); err != nil {
			L.RaiseError("stopAll %q: %v", queryRef, err)
			return 0
		}
		return 0
	}))
	L.SetField(ctx, "scripts", scriptsTbl)
	return ctx
}

func scriptControlArgIndexes(L *lua.LState) (int, int) {
	if _, ok := L.Get(1).(*lua.LTable); ok {
		return 2, 3
	}
	return 1, 2
}

func scriptControlQueryRef(v lua.LValue) string {
	switch value := v.(type) {
	case lua.LString:
		return string(value)
	case *lua.LTable:
		return lua.LVAsString(value.RawGetString("queryRef"))
	default:
		return ""
	}
}

func (rt *activationRuntime) scheduleAfter(d time.Duration, fn func()) int64 {
	var id int64
	t := time.AfterFunc(d, func() {
		rt.vm.timers.cancel(id)
		rt.vm.enqueue(fn)
	})
	id = rt.vm.timers.add(t)
	return id
}

func (rt *activationRuntime) scheduleEvery(spec triggerSpec, fn func()) int64 {
	var id int64
	var schedule func()
	schedule = func() {
		delay := spec.min
		if spec.max > spec.min {
			delay += time.Duration(rt.random.Int63n(int64(spec.max - spec.min)))
		}
		next := time.Now().Add(delay)
		rt.nextFireAt = &next
		rt.engine.saveInstanceRecord(rt.instanceHash(), func(inst *scriptstore.Instance) {
			inst.NextFireAt = timePtr(next)
		})
		t := time.AfterFunc(delay, func() {
			rt.vm.timers.cancel(id)
			rt.vm.enqueue(func() {
				fn()
				select {
				case <-rt.vm.done:
				default:
					schedule()
				}
			})
		})
		id = rt.vm.timers.add(t)
	}
	schedule()
	return id
}

func (rt *activationRuntime) instanceHash() string {
	return scriptstore.HashInstance(rt.name, rt.queryRef)
}

func (rt *activationRuntime) markFired(targets []domain.Entity) {
	now := time.Now()
	keys := make([]string, 0, len(targets))
	for _, target := range targets {
		keys = append(keys, target.Key())
	}
	rt.engine.saveInstanceRecord(rt.instanceHash(), func(inst *scriptstore.Instance) {
		inst.Status = "running"
		inst.LastFiredAt = timePtr(now)
		inst.ResolvedTargets = keys
		inst.LastError = ""
		inst.FireCount++
	})
}

func (rt *activationRuntime) markError(err error) {
	rt.engine.saveInstanceRecord(rt.instanceHash(), func(inst *scriptstore.Instance) {
		inst.LastError = err.Error()
	})
}

func triggerInfo(spec triggerSpec) scriptstore.TriggerInfo {
	return scriptstore.TriggerInfo{
		Kind:       spec.kind,
		QueryRef:   spec.queryRef,
		Query:      queryIdentity(spec.query),
		MinSeconds: spec.min.Seconds(),
		MaxSeconds: spec.max.Seconds(),
	}
}

func targetInfo(spec targetSpec) scriptstore.TargetInfo {
	return scriptstore.TargetInfo{
		Kind:     spec.kind,
		QueryRef: spec.queryRef,
		Query:    queryIdentity(spec.query),
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func queryIdentity(query storage.Query) string {
	if query.Pattern == "" && len(query.Where) == 0 {
		return ""
	}
	data, err := json.Marshal(query)
	if err != nil {
		return ""
	}
	return string(data)
}

type ErrNotFound struct{ Name string }

func (e *ErrNotFound) Error() string {
	return "sb-script: no definition for " + e.Name
}
