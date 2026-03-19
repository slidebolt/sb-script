package engine

import (
	"encoding/json"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"

	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	scriptstore "github.com/slidebolt/sb-script/internal/store"
	storage "github.com/slidebolt/sb-storage-sdk"
	lua "github.com/yuin/gopher-lua"
)

// Engine manages passive automation definitions and active automation VMs.
type Engine struct {
	msg   messenger.Messenger
	store storage.Storage

	mu          sync.RWMutex
	definitions map[string]string // name -> lua source
	instances   map[string]*luaVM // hash(name+query) -> active automation VM
}

func New(msg messenger.Messenger, store storage.Storage) (*Engine, error) {
	e := &Engine{
		msg:         msg,
		store:       store,
		definitions: make(map[string]string),
		instances:   make(map[string]*luaVM),
	}

	entries, err := store.Search("sb-script.definitions.>")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		var def storedDef
		if err := json.Unmarshal(entry.Data, &def); err != nil {
			slog.Warn("sb-script: skip bad definition", "key", entry.Key, "err", err)
			continue
		}
		e.definitions[def.Name] = def.Source
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
		if err := e.startInstance(inst.Name, inst.Query); err != nil {
			slog.Warn("sb-script: resume instance error", "name", inst.Name, "query", inst.Query, "err", err)
		}
	}

	return e, nil
}

func (e *Engine) SaveDefinition(name, source string) error {
	def := storedDef{Name: name, Source: source}
	if err := e.store.Save(def); err != nil {
		return err
	}
	e.mu.Lock()
	e.definitions[name] = source
	e.mu.Unlock()
	return nil
}

func (e *Engine) DeleteDefinition(name string) error {
	e.mu.Lock()
	for hash, vm := range e.instances {
		inst := e.instanceRecord(hash)
		if inst != nil && inst.Name == name {
			vm.close()
			delete(e.instances, hash)
			e.store.Delete(scriptstore.InstanceKey{Hash: hash})
		}
	}
	delete(e.definitions, name)
	e.mu.Unlock()

	return e.store.Delete(scriptstore.DefinitionKey{Name: name})
}

func (e *Engine) StartScript(name, query string) (string, error) {
	hash := scriptstore.HashInstance(name, query)
	e.mu.RLock()
	_, exists := e.instances[hash]
	e.mu.RUnlock()
	if exists {
		return hash, nil
	}
	return hash, e.startInstance(name, query)
}

func (e *Engine) StopScript(name, query string) error {
	hash := scriptstore.HashInstance(name, query)
	e.mu.Lock()
	vm, ok := e.instances[hash]
	if ok {
		vm.close()
		delete(e.instances, hash)
	}
	e.mu.Unlock()
	if ok {
		e.store.Delete(scriptstore.InstanceKey{Hash: hash})
	}
	return nil
}

func (e *Engine) Shutdown() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, vm := range e.instances {
		vm.close()
	}
}

type storedDef struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

func (d storedDef) Key() string { return scriptstore.DefinitionKey{Name: d.Name}.Key() }

func (e *Engine) startInstance(name, query string) error {
	e.mu.RLock()
	source, ok := e.definitions[name]
	e.mu.RUnlock()
	if !ok || source == "" {
		return &ErrNotFound{Name: name}
	}

	vm := newLuaVM()
	vm.injectServices(e.msg, e.store, e)

	rt := &activationRuntime{
		engine:        e,
		msg:           e.msg,
		store:         e.store,
		vm:            vm,
		name:          name,
		queryOverride: query,
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

	hash := scriptstore.HashInstance(name, query)
	e.mu.Lock()
	e.instances[hash] = vm
	e.mu.Unlock()

	inst := scriptstore.Instance{Name: name, Query: query}
	e.store.Save(inst)
	return nil
}

func (e *Engine) instanceRecord(hash string) *scriptstore.Instance {
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

type automationSpec struct {
	trigger triggerSpec
	targets targetSpec
}

type triggerSpec struct {
	kind  string
	query string
	min   time.Duration
	max   time.Duration
}

type targetSpec struct {
	kind  string
	query string
}

type activationRuntime struct {
	engine        *Engine
	msg           messenger.Messenger
	store         storage.Storage
	vm            *luaVM
	name          string
	queryOverride string
	activated     bool
	random        *rand.Rand
}

func (rt *activationRuntime) injectAutomationAPI() {
	L := rt.vm.L

	L.SetGlobal("Entity", L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(1)
		tbl := L.NewTable()
		L.SetField(tbl, "kind", lua.LString("entity"))
		L.SetField(tbl, "query", lua.LString(key))
		L.Push(tbl)
		return 1
	}))

	L.SetGlobal("Query", L.NewFunction(func(L *lua.LState) int {
		query := L.CheckString(1)
		tbl := L.NewTable()
		L.SetField(tbl, "kind", lua.LString("query"))
		L.SetField(tbl, "query", lua.LString(query))
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
		spec := parseAutomationSpec(L, specTbl, rt.queryOverride)
		rt.activate(spec, fn)
		rt.activated = true
		return 0
	}))
}

func parseAutomationSpec(L *lua.LState, specTbl *lua.LTable, queryOverride string) automationSpec {
	spec := automationSpec{
		targets: targetSpec{kind: "none"},
	}
	if trg, ok := L.GetField(specTbl, "trigger").(*lua.LTable); ok {
		spec.trigger = parseTriggerSpec(L, trg)
	}
	if queryOverride != "" {
		spec.targets = targetSpec{kind: "query", query: queryOverride}
	} else if tgt, ok := L.GetField(specTbl, "targets").(*lua.LTable); ok {
		spec.targets = parseTargetSpec(L, tgt)
	}
	return spec
}

func parseTriggerSpec(L *lua.LState, tbl *lua.LTable) triggerSpec {
	spec := triggerSpec{kind: lua.LVAsString(L.GetField(tbl, "kind"))}
	spec.query = lua.LVAsString(L.GetField(tbl, "query"))
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
	return targetSpec{
		kind:  lua.LVAsString(L.GetField(tbl, "kind")),
		query: lua.LVAsString(L.GetField(tbl, "query")),
	}
}

func (rt *activationRuntime) activate(spec automationSpec, fn *lua.LFunction) {
	switch spec.trigger.kind {
	case "entity":
		subject := "state.changed." + spec.trigger.query
		sub, err := rt.msg.Subscribe(subject, func(m *messenger.Message) {
			var ent domain.Entity
			if err := json.Unmarshal(m.Data, &ent); err != nil {
				return
			}
			rt.invoke(fn, spec, &ent)
		})
		if err == nil {
			rt.vm.subs = append(rt.vm.subs, sub)
		}
	case "query":
		sub, err := rt.msg.Subscribe("state.changed.>", func(m *messenger.Message) {
			var ent domain.Entity
			if err := json.Unmarshal(m.Data, &ent); err != nil {
				return
			}
			if !entityMatchesQuery(ent, spec.trigger.query) {
				return
			}
			rt.invoke(fn, spec, &ent)
		})
		if err == nil {
			rt.vm.subs = append(rt.vm.subs, sub)
		}
	case "interval":
		rt.scheduleEvery(spec.trigger, func() {
			rt.invoke(fn, spec, nil)
		})
	}
}

func (rt *activationRuntime) invoke(fn *lua.LFunction, spec automationSpec, trigger *domain.Entity) {
	targets := rt.resolveTargets(spec.targets)
	rt.vm.enqueue(func() {
		ctx := rt.newContext(targets, trigger)
		if err := rt.vm.L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, ctx); err != nil {
			slog.Warn("sb-script: Automation callback error", "name", rt.name, "err", err)
		}
	})
}

func (rt *activationRuntime) resolveTargets(spec targetSpec) []domain.Entity {
	switch spec.kind {
	case "entity", "query":
		targets, err := resolveQuery(rt.store, spec.query)
		if err != nil {
			slog.Warn("sb-script: resolve targets error", "name", rt.name, "query", spec.query, "err", err)
			return nil
		}
		return targets
	default:
		return nil
	}
}

func (rt *activationRuntime) newContext(targets []domain.Entity, trigger *domain.Entity) *lua.LTable {
	L := rt.vm.L
	ctx := L.NewTable()
	L.SetField(ctx, "targets", entitiesToTable(L, targets))

	triggerTbl := L.NewTable()
	if trigger != nil {
		L.SetField(triggerTbl, "entity", entityToTable(L, *trigger))
	}
	L.SetField(ctx, "trigger", triggerTbl)

	L.SetField(ctx, "query", L.NewFunction(func(L *lua.LState) int {
		query := L.CheckString(1)
		entities, err := resolveQuery(rt.store, query)
		if err != nil {
			L.Push(L.NewTable())
			return 1
		}
		L.Push(entitiesToTable(L, entities))
		return 1
	}))
	L.SetField(ctx, "queryOne", L.NewFunction(func(L *lua.LState) int {
		query := L.CheckString(1)
		entities, err := resolveQuery(rt.store, query)
		if err != nil || len(entities) == 0 {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(entityToTable(L, entities[0]))
		return 1
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
		key := lua.LVAsString(L.GetField(entityTbl, "key"))
		_ = rt.msg.Publish(key+".command."+action, paramsJSON)
		return 0
	}))
	L.SetField(ctx, "after", L.NewFunction(func(L *lua.LState) int {
		secs := float64(L.CheckNumber(1))
		cb := L.CheckFunction(2)
		id := rt.scheduleAfter(durationFromSecs(secs), func() {
			child := rt.newContext(targets, trigger)
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
			child := rt.newContext(targets, trigger)
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
	return ctx
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

func entityMatchesQuery(ent domain.Entity, query string) bool {
	if len(query) > 0 && query[0] == '?' {
		filters, err := parseFilters(query[1:])
		if err != nil {
			return false
		}
		data, err := json.Marshal(ent)
		if err != nil {
			return false
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			return false
		}
		return filtersMatch(doc, filters)
	}
	return matchesPattern(ent.Key(), query)
}

func filtersMatch(doc map[string]any, filters []storage.Filter) bool {
	for _, f := range filters {
		v := getField(doc, strings.Split(f.Field, "."))
		if !filterMatch(v, f) {
			return false
		}
	}
	return true
}

func filterMatch(v any, f storage.Filter) bool {
	if f.Op != storage.Eq {
		return false
	}
	switch tv := v.(type) {
	case bool:
		if bv, ok := f.Value.(bool); ok {
			return tv == bv
		}
	case float64:
		switch fv := f.Value.(type) {
		case float64:
			return tv == fv
		case int:
			return tv == float64(fv)
		}
	case string:
		if sv, ok := f.Value.(string); ok {
			return tv == sv
		}
	}
	return false
}

func getField(doc map[string]any, path []string) any {
	if len(path) == 0 {
		return nil
	}
	v, ok := doc[path[0]]
	if !ok {
		return nil
	}
	if len(path) == 1 {
		return v
	}
	if sub, ok := v.(map[string]any); ok {
		return getField(sub, path[1:])
	}
	return nil
}

func matchesPattern(key, pattern string) bool {
	if pattern == "" || pattern == ">" {
		return true
	}
	kp := strings.Split(key, ".")
	pp := strings.Split(pattern, ".")
	return matchSegments(kp, pp)
}

func matchSegments(key, pat []string) bool {
	if len(pat) == 0 {
		return len(key) == 0
	}
	if pat[len(pat)-1] == ">" {
		if len(key) < len(pat)-1 {
			return false
		}
		return matchSegments(key[:len(pat)-1], pat[:len(pat)-1])
	}
	if len(key) != len(pat) {
		return false
	}
	for i := range pat {
		if pat[i] != "*" && pat[i] != key[i] {
			return false
		}
	}
	return true
}

type ErrNotFound struct{ Name string }

func (e *ErrNotFound) Error() string {
	return "sb-script: no definition for " + e.Name
}
