package engine

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"

	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	storage "github.com/slidebolt/sb-storage-sdk"
	lua "github.com/yuin/gopher-lua"
)

const minTimerInterval = 50 * time.Millisecond

// luaVM wraps a gopher-lua LState with a work-queue goroutine.
type luaVM struct {
	*vm
	L         *lua.LState
	timers    *timerSet
	subs      []messenger.Subscription
	closeOnce sync.Once
}

func newLuaVM() *luaVM {
	return &luaVM{
		vm:     newVM(),
		L:      lua.NewState(),
		timers: newTimerSet(),
	}
}

// close tears down the VM: cancels timers, unsubscribes NATS, stops goroutine.
// Safe to call multiple times.
func (lv *luaVM) close() {
	lv.closeOnce.Do(func() {
		lv.timers.cancelAll()
		for _, sub := range lv.subs {
			sub.Unsubscribe()
		}
		lv.subs = nil
		lv.vm.stop()
		lv.L.Close()
	})
}

// exec runs fn on the VM's goroutine and blocks until complete.
func (lv *luaVM) exec(fn func()) {
	done := make(chan struct{})
	lv.enqueue(func() {
		fn()
		close(done)
	})
	<-done
}

// injectServices wires QueryService, CommandService, TimerService and Log into L.
func (lv *luaVM) injectServices(msg messenger.Messenger, store storage.Storage, e *Engine) {
	L := lv.L

	// --- QueryService ---
	qs := L.NewTable()
	L.SetField(qs, "Find", L.NewFunction(func(L *lua.LState) int {
		query, err := queryFromLuaValue(L.Get(1))
		if err != nil {
			slog.Warn("sb-script: QueryService.Find error", "err", err)
			L.Push(L.NewTable())
			return 1
		}
		entities, err := resolveQuery(store, query)
		if err != nil {
			slog.Warn("sb-script: QueryService.Find error", "err", err)
			L.Push(L.NewTable())
			return 1
		}
		L.Push(entitiesToTable(L, entities))
		return 1
	}))
	L.SetField(qs, "FindOne", L.NewFunction(func(L *lua.LState) int {
		query, err := queryFromLuaValue(L.Get(1))
		if err != nil {
			L.Push(lua.LNil)
			return 1
		}
		entities, err := resolveQuery(store, query)
		if err != nil || len(entities) == 0 {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(entityToTable(L, entities[0]))
		return 1
	}))
	L.SetGlobal("QueryService", qs)

	// --- CommandService ---
	cs := L.NewTable()
	L.SetField(cs, "Send", L.NewFunction(func(L *lua.LState) int {
		entityTbl := L.CheckTable(1)
		action := L.CheckString(2)
		var paramsJSON []byte
		if L.GetTop() >= 3 {
			paramsTbl, ok := L.Get(3).(*lua.LTable)
			if ok {
				m := luaTableToMap(paramsTbl)
				paramsJSON, _ = json.Marshal(m)
			}
		}
		if paramsJSON == nil {
			paramsJSON = []byte("{}")
		}

		key := lua.LVAsString(L.GetField(entityTbl, "key"))
		subject := key + ".command." + action
		if err := msg.Publish(subject, paramsJSON); err != nil {
			slog.Warn("sb-script: CommandService.Send error", "subject", subject, "err", err)
		}
		return 0
	}))
	L.SetGlobal("CommandService", cs)

	// --- TimerService ---
	ts := L.NewTable()
	L.SetField(ts, "After", L.NewFunction(func(L *lua.LState) int {
		secs := float64(L.CheckNumber(1))
		fn := L.CheckFunction(2)
		dur := durationFromSecs(secs)
		var id int64
		t := time.AfterFunc(dur, func() {
			lv.timers.cancel(id)
			lv.enqueue(func() {
				if err := L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}); err != nil {
					slog.Warn("sb-script: TimerService.After callback error", "err", err)
				}
			})
		})
		id = lv.timers.add(t)
		L.Push(lua.LNumber(id))
		return 1
	}))
	L.SetField(ts, "Every", L.NewFunction(func(L *lua.LState) int {
		secs := float64(L.CheckNumber(1))
		fn := L.CheckFunction(2)
		dur := durationFromSecs(secs)
		var id int64
		var schedule func()
		schedule = func() {
			t := time.AfterFunc(dur, func() {
				lv.timers.cancel(id)
				lv.enqueue(func() {
					if err := L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}); err != nil {
						slog.Warn("sb-script: TimerService.Every callback error", "err", err)
					}
					select {
					case <-lv.vm.done:
					default:
						t2 := time.AfterFunc(dur, func() {
							lv.enqueue(func() {
								schedule()
							})
						})
						id = lv.timers.add(t2)
					}
				})
			})
			id = lv.timers.add(t)
		}
		schedule()
		L.Push(lua.LNumber(id))
		return 1
	}))
	L.SetField(ts, "Cancel", L.NewFunction(func(L *lua.LState) int {
		id := int64(L.CheckNumber(1))
		lv.timers.cancel(id)
		return 0
	}))
	L.SetGlobal("TimerService", ts)

	// --- Log ---
	log := L.NewTable()
	for _, level := range []struct {
		name string
		fn   func(string, ...any)
	}{
		{"Info", slog.Info},
		{"Warn", slog.Warn},
		{"Error", slog.Error},
		{"Debug", slog.Debug},
	} {
		lvl := level
		L.SetField(log, lvl.name, L.NewFunction(func(L *lua.LState) int {
			msg := L.CheckString(1)
			lvl.fn("sb-script: " + msg)
			return 0
		}))
	}
	L.SetGlobal("Log", log)

	// Override print → slog.Info
	L.SetGlobal("print", L.NewFunction(func(L *lua.LState) int {
		msg := L.CheckString(1)
		slog.Info("sb-script: " + msg)
		return 0
	}))
}

// --- Helpers ---

// resolveQuery resolves a structured storage query to a slice of entities.
func resolveQuery(store storage.Storage, q storage.Query) ([]domain.Entity, error) {
	entries, err := store.Query(q)
	if err != nil {
		return nil, err
	}

	entities := make([]domain.Entity, 0, len(entries))
	for _, e := range entries {
		var ent domain.Entity
		if err := json.Unmarshal(e.Data, &ent); err == nil {
			entities = append(entities, ent)
		}
	}
	return entities, nil
}

func queryFromLuaValue(v lua.LValue) (storage.Query, error) {
	switch t := v.(type) {
	case lua.LString:
		return storage.Query{Pattern: string(t)}, nil
	case *lua.LTable:
		if querySpec, ok := t.RawGetString("query").(*lua.LTable); ok {
			return queryFromLuaTable(querySpec)
		}
		return queryFromLuaTable(t)
	default:
		return storage.Query{}, fmt.Errorf("unsupported query value %T", v)
	}
}

func queryFromLuaTable(tbl *lua.LTable) (storage.Query, error) {
	body, err := json.Marshal(luaTableToAny(tbl))
	if err != nil {
		return storage.Query{}, err
	}
	var query storage.Query
	if err := json.Unmarshal(body, &query); err != nil {
		return storage.Query{}, err
	}
	return query, nil
}

func queryToMap(q storage.Query) map[string]any {
	m := make(map[string]any)
	if q.Pattern != "" {
		m["pattern"] = q.Pattern
	}
	if len(q.Where) > 0 {
		where := make([]map[string]any, 0, len(q.Where))
		for _, filter := range q.Where {
			item := map[string]any{
				"field": filter.Field,
				"op":    filter.Op,
			}
			if filter.Value != nil {
				item["value"] = filter.Value
			}
			where = append(where, item)
		}
		m["where"] = where
	}
	return m
}

// entitiesToTable converts a []domain.Entity to a Lua table with an :each method.
func entitiesToTable(L *lua.LState, entities []domain.Entity) *lua.LTable {
	tbl := L.NewTable()
	for i, e := range entities {
		L.RawSetInt(tbl, i+1, entityToTable(L, e))
	}
	mt := L.NewTable()
	L.SetField(mt, "__index", mt)
	L.SetField(mt, "each", L.NewFunction(func(L *lua.LState) int {
		arr := L.CheckTable(1)
		fn := L.CheckFunction(2)
		arr.ForEach(func(_, v lua.LValue) {
			if err := L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, v); err != nil {
				slog.Warn("sb-script: each callback error", "err", err)
			}
		})
		return 0
	}))
	L.SetMetatable(tbl, mt)
	return tbl
}

// entityToTable converts a domain.Entity to a Lua table.
func entityToTable(L *lua.LState, e domain.Entity) *lua.LTable {
	tbl := L.NewTable()
	L.SetField(tbl, "plugin", lua.LString(e.Plugin))
	L.SetField(tbl, "deviceID", lua.LString(e.DeviceID))
	L.SetField(tbl, "id", lua.LString(e.ID))
	L.SetField(tbl, "key", lua.LString(e.Key()))
	L.SetField(tbl, "type", lua.LString(e.Type))
	L.SetField(tbl, "name", lua.LString(e.Name))
	if len(e.Commands) > 0 {
		cmds := L.NewTable()
		for i, cmd := range e.Commands {
			L.RawSetInt(cmds, i+1, lua.LString(cmd))
		}
		L.SetField(tbl, "commands", cmds)
	} else {
		L.SetField(tbl, "commands", L.NewTable())
	}

	if e.State != nil {
		data, _ := json.Marshal(e.State)
		stateTbl := jsonToLua(L, data)
		L.SetField(tbl, "state", stateTbl)
	} else {
		L.SetField(tbl, "state", L.NewTable())
	}
	return tbl
}

// jsonToLua converts raw JSON into a Lua value (table, string, number, bool, nil).
func jsonToLua(L *lua.LState, data []byte) lua.LValue {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return lua.LNil
	}
	return anyToLua(L, v)
}

func anyToLua(L *lua.LState, v any) lua.LValue {
	if v == nil {
		return lua.LNil
	}
	switch t := v.(type) {
	case bool:
		if t {
			return lua.LTrue
		}
		return lua.LFalse
	case float64:
		return lua.LNumber(t)
	case string:
		return lua.LString(t)
	case map[string]any:
		tbl := L.NewTable()
		for k, val := range t {
			L.SetField(tbl, k, anyToLua(L, val))
		}
		return tbl
	case []any:
		tbl := L.NewTable()
		for i, val := range t {
			L.RawSetInt(tbl, i+1, anyToLua(L, val))
		}
		return tbl
	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Map:
			if rv.Type().Key().Kind() != reflect.String {
				return lua.LNil
			}
			tbl := L.NewTable()
			iter := rv.MapRange()
			for iter.Next() {
				L.SetField(tbl, iter.Key().String(), anyToLua(L, iter.Value().Interface()))
			}
			return tbl
		case reflect.Slice, reflect.Array:
			tbl := L.NewTable()
			for i := 0; i < rv.Len(); i++ {
				L.RawSetInt(tbl, i+1, anyToLua(L, rv.Index(i).Interface()))
			}
			return tbl
		default:
			return lua.LNil
		}
	}
}

// luaTableToMap converts a Lua table to a map[string]any (shallow).
func luaTableToMap(tbl *lua.LTable) map[string]any {
	m := make(map[string]any)
	tbl.ForEach(func(k, v lua.LValue) {
		key := lua.LVAsString(k)
		m[key] = luaValueToAny(v)
	})
	return m
}

// luaTableToAny converts a Lua table to either a []any (array) or map[string]any (object).
// A table is treated as an array when it has only consecutive integer keys starting at 1.
func luaTableToAny(tbl *lua.LTable) any {
	n := tbl.Len()
	if n > 0 {
		isArray := true
		tbl.ForEach(func(k, _ lua.LValue) {
			if num, ok := k.(lua.LNumber); !ok || int(num) < 1 || int(num) > n {
				isArray = false
			}
		})
		if isArray {
			arr := make([]any, n)
			for i := 1; i <= n; i++ {
				arr[i-1] = luaValueToAny(tbl.RawGetInt(i))
			}
			return arr
		}
	}
	return luaTableToMap(tbl)
}

func luaValueToAny(v lua.LValue) any {
	switch t := v.(type) {
	case lua.LBool:
		return bool(t)
	case lua.LNumber:
		return float64(t)
	case lua.LString:
		return string(t)
	case *lua.LTable:
		return luaTableToAny(t)
	default:
		return nil
	}
}

func durationFromSecs(secs float64) time.Duration {
	d := time.Duration(secs * float64(time.Second))
	if d < minTimerInterval {
		d = minTimerInterval
	}
	return d
}
