package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	domain "github.com/slidebolt/sb-domain"
	logcfg "github.com/slidebolt/sb-logging"
	logging "github.com/slidebolt/sb-logging-sdk"
	logserver "github.com/slidebolt/sb-logging/server"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	scriptstore "github.com/slidebolt/sb-script/internal/store"
	storage "github.com/slidebolt/sb-storage-sdk"
	storageserver "github.com/slidebolt/sb-storage-server"
	lua "github.com/yuin/gopher-lua"
)

func TestSaveDefinition_PersistsCanonicalScriptTree(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	dir := t.TempDir()
	handler, err := storageserver.NewHandlerWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.Register(msg); err != nil {
		t.Fatal(err)
	}
	store := storage.ClientFrom(msg)

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Automation("PartyTime", {
		trigger = Interval(1),
		targets = None()
	}, function(ctx)
	end)`
	if err := saveDefinition(t, store, "party_time", source); err != nil {
		t.Fatal(err)
	}

	rootPath := filepath.Join(dir, "sb-script", "sb-script.json")
	scriptsPath := filepath.Join(dir, "sb-script", "scripts", "scripts.json")
	defJSONPath := filepath.Join(dir, "sb-script", "scripts", "party_time", "party_time.json")
	defLuaPath := filepath.Join(dir, "sb-script", "scripts", "party_time", "party_time.lua")
	for _, p := range []string{rootPath, scriptsPath, defJSONPath, defLuaPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected persisted path %s: %v", p, err)
		}
	}

	raw, err := store.Get(scriptstore.DefinitionKey{Name: "party_time"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"source":"Automation(\"PartyTime\"`) {
		t.Fatalf("expected merged source in public get, got %s", raw)
	}

	luaBody, err := os.ReadFile(defLuaPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(luaBody) != source {
		t.Fatalf("lua body mismatch:\n%s", luaBody)
	}
}

func TestStartScript_PersistsRuntimeMetadata(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ent := domain.Entity{
		ID:       "light1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "light",
		Name:     "Lamp",
		State:    domain.Light{Power: true, Brightness: 100},
	}
	if err := store.Save(ent); err != nil {
		t.Fatal(err)
	}

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Automation("MetaScript", {
		trigger = Interval(0.05),
		targets = Query({ pattern = "plugin.dev1.original" })
	}, function(ctx)
	end)`
	if err := saveDefinition(t, store, "MetaScript", source); err != nil {
		t.Fatal(err)
	}
	if err := storage.EnsureQueryLayout(store); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveQueryDefinition(store, "meta_targets", storage.Query{Pattern: "plugin.dev1.light1"}); err != nil {
		t.Fatal(err)
	}

	hash, err := engine.StartScript("MetaScript", "meta_targets")
	if err != nil {
		t.Fatal(err)
	}

	inst := waitForInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.FireCount > 0 && inst.LastFiredAt != nil && len(inst.ResolvedTargets) == 1
	})

	if inst.Hash != hash {
		t.Fatalf("hash: got %q want %q", inst.Hash, hash)
	}
	if inst.Name != "MetaScript" || inst.QueryRef != "meta_targets" {
		t.Fatalf("unexpected instance identity: %+v", inst)
	}
	if inst.Status != "running" {
		t.Fatalf("status: got %q want running", inst.Status)
	}
	if inst.Trigger.Kind != "interval" {
		t.Fatalf("trigger kind: got %q want interval", inst.Trigger.Kind)
	}
	if inst.Trigger.MinSeconds <= 0 || inst.Trigger.MaxSeconds <= 0 {
		t.Fatalf("unexpected trigger interval: %+v", inst.Trigger)
	}
	if inst.Targets.Kind != "query_ref" || inst.Targets.QueryRef != "meta_targets" {
		t.Fatalf("unexpected targets: %+v", inst.Targets)
	}
	if inst.StartedAt == nil || inst.LastFiredAt == nil || inst.NextFireAt == nil {
		t.Fatalf("expected timestamps, got %+v", inst)
	}
	if inst.LastFiredAt.Before(*inst.StartedAt) {
		t.Fatalf("last fire before start: started=%v fired=%v", inst.StartedAt, inst.LastFiredAt)
	}
	if got := inst.ResolvedTargets[0]; got != ent.Key() {
		t.Fatalf("resolved target: got %q want %q", got, ent.Key())
	}
}

func TestStartScriptMissingReturnsPromptly(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	errCh := make(chan error, 1)
	go func() {
		_, err := engine.StartScript("missing_script", "")
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected missing script error")
		}
		if !strings.Contains(err.Error(), "missing_script") {
			t.Fatalf("unexpected start error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("StartScript timed out for missing script")
	}
}

func TestStopScriptMissingReturnsPromptly(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	errCh := make(chan error, 1)
	go func() {
		errCh <- engine.StopScript("missing_script", "")
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected stop error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("StopScript timed out for missing script")
	}
}

func TestScriptCanStopItselfWithoutDeadlockingEngine(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Automation("SelfStop", {
		trigger = Interval(0.05),
		targets = None()
	}, function(ctx)
		ctx.scripts:stop("SelfStop")
	end)`
	if err := saveDefinition(t, store, "SelfStop", source); err != nil {
		t.Fatal(err)
	}

	hash, err := engine.StartScript("SelfStop", "")
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := store.Get(scriptstore.InstanceKey{Hash: hash}); err != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := store.Get(scriptstore.InstanceKey{Hash: hash}); err == nil {
		t.Fatalf("timed out waiting for self-stopping script instance %s to disappear", hash)
	}

	done := make(chan error, 1)
	go func() {
		_, err := engine.StartScript("missing_script", "")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected missing script error after self-stop")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("engine remained blocked after self-stop")
	}
}

func TestQueryTriggerUsesStructuredStorageWatch(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Automation("watch_switches", {
		trigger = Query({
			where = {
				{ field = "type", op = "eq", value = "switch" },
				{ field = "state.power", op = "eq", value = true }
			}
		}),
		targets = None()
	}, function(ctx)
	end)`
	if err := saveDefinition(t, store, "watch_switches", source); err != nil {
		t.Fatal(err)
	}

	hash, err := engine.StartScript("watch_switches", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "test",
		DeviceID: "dev1",
		Type:     "switch",
		Name:     "Switch",
		State:    domain.Switch{Power: true},
	}); err != nil {
		t.Fatal(err)
	}

	inst := waitForInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.FireCount > 0
	})
	if inst.Trigger.Kind != "query" {
		t.Fatalf("trigger kind: got %q want query", inst.Trigger.Kind)
	}
}

func TestQueryRefTargetsResolveFromStorage(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Save(domain.Entity{
		ID:       "light1",
		Plugin:   "test",
		DeviceID: "dev1",
		Type:     "light",
		Name:     "Light",
		State:    domain.Light{Power: true, ColorMode: "rgb"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(domain.Entity{
		ID:       "light2",
		Plugin:   "test",
		DeviceID: "dev2",
		Type:     "light",
		Name:     "Warm Light",
		State:    domain.Light{Power: true, ColorMode: "color_temp"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := storage.EnsureQueryLayout(store); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveQueryDefinition(store, "rgb_lights", storage.Query{
		Where: []storage.Filter{
			{Field: "type", Op: storage.Eq, Value: "light"},
			{Field: "state.colorMode", Op: storage.Eq, Value: "rgb"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Automation("query_ref_targets", {
		trigger = Interval(0.05),
		targets = QueryRef("rgb_lights")
	}, function(ctx)
	end)`
	if err := saveDefinition(t, store, "query_ref_targets", source); err != nil {
		t.Fatal(err)
	}

	hash, err := engine.StartScript("query_ref_targets", "")
	if err != nil {
		t.Fatal(err)
	}

	inst := waitForInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.Targets.Kind == "query_ref" &&
			inst.Targets.QueryRef == "rgb_lights" &&
			strings.Contains(inst.Targets.Query, "\"where\"") &&
			len(inst.ResolvedTargets) == 1
	})
	if inst.Targets.Kind != "query_ref" || inst.Targets.QueryRef != "rgb_lights" {
		t.Fatalf("unexpected targets: %+v", inst.Targets)
	}
	if !strings.Contains(inst.Targets.Query, "\"state.colorMode\"") {
		t.Fatalf("targets query = %s, want state.colorMode filter preserved", inst.Targets.Query)
	}
	if len(inst.ResolvedTargets) != 1 || inst.ResolvedTargets[0] != "test.dev1.light1" {
		t.Fatalf("resolved targets = %v, want only test.dev1.light1", inst.ResolvedTargets)
	}
}

func TestQueryRefCanBeUsedWithContextQueryOne(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := storage.EnsureQueryLayout(store); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveQueryDefinition(store, "party_level", storage.Query{
		Pattern: "plugin-virtual.dev1.slider",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(domain.Entity{
		ID:       "slider",
		Plugin:   "plugin-virtual",
		DeviceID: "dev1",
		Type:     "number",
		Name:     "Party Level",
		State:    domain.Number{Value: 80, Min: 0, Max: 100, Step: 1},
	}); err != nil {
		t.Fatal(err)
	}

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Automation("query_ref_context_lookup", {
		trigger = Interval(0.05),
		targets = None()
	}, function(ctx)
		local slider = ctx.queryOne(QueryRef("party_level"))
		if slider ~= nil and slider.state ~= nil and slider.state.value == 80 then
			print("query ref lookup ok")
		end
	end)`
	if err := saveDefinition(t, store, "query_ref_context_lookup", source); err != nil {
		t.Fatal(err)
	}

	hash, err := engine.StartScript("query_ref_context_lookup", "")
	if err != nil {
		t.Fatal(err)
	}

	inst := waitForInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.FireCount > 0 && inst.LastError == ""
	})
	if inst.LastError != "" {
		t.Fatalf("unexpected script error: %s", inst.LastError)
	}
}

func TestEntityToTableExposesCommands(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	tbl := entityToTable(L, domain.Entity{
		ID:       "light1",
		Plugin:   "plugin-wiz",
		DeviceID: "wiz-001",
		Type:     "light",
		Name:     "Lamp",
		Commands: []string{"light_turn_on", "light_set_rgb"},
		State:    domain.Light{Power: true},
	})

	cmds, ok := L.GetField(tbl, "commands").(*lua.LTable)
	if !ok {
		t.Fatalf("commands field type = %T, want *lua.LTable", L.GetField(tbl, "commands"))
	}
	if got := lua.LVAsString(cmds.RawGetInt(1)); got != "light_turn_on" {
		t.Fatalf("commands[1] = %q, want light_turn_on", got)
	}
	if got := lua.LVAsString(cmds.RawGetInt(2)); got != "light_set_rgb" {
		t.Fatalf("commands[2] = %q, want light_set_rgb", got)
	}
}

func TestContextScriptsStartAndStopControlOtherScriptInstances(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := storage.EnsureQueryLayout(store); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveQueryDefinition(store, "control_switch", storage.Query{
		Pattern: "plugin-virtual.dev1.party_switch",
	}); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveQueryDefinition(store, "target_lights", storage.Query{
		Pattern: "plugin.dev1.light1",
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.Save(domain.Entity{
		ID:       "party_switch",
		Plugin:   "plugin-virtual",
		DeviceID: "dev1",
		Type:     "switch",
		Name:     "Party Switch",
		State:    domain.Switch{Power: false},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(domain.Entity{
		ID:       "light1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "light",
		Name:     "Lamp",
		State:    domain.Light{Power: true},
	}); err != nil {
		t.Fatal(err)
	}

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	partySource := `Automation("PartyTime", {
		trigger = Interval(0.05),
		targets = QueryRef("target_lights")
	}, function(ctx)
	end)`
	if err := saveDefinition(t, store, "PartyTime", partySource); err != nil {
		t.Fatal(err)
	}

	controllerSource := `Automation("BasementPartySwitch", {
		trigger = QueryRef("control_switch"),
		targets = None()
	}, function(ctx)
		if ctx.trigger.entity.state.power then
			ctx.scripts:start("PartyTime", "target_lights")
		else
			ctx.scripts:stop("PartyTime", "target_lights")
		end
	end)`
	if err := saveDefinition(t, store, "BasementPartySwitch", controllerSource); err != nil {
		t.Fatal(err)
	}

	controllerHash, err := engine.StartScript("BasementPartySwitch", "")
	if err != nil {
		t.Fatal(err)
	}
	if controllerHash == "" {
		t.Fatal("expected controller hash")
	}

	if err := store.Save(domain.Entity{
		ID:       "party_switch",
		Plugin:   "plugin-virtual",
		DeviceID: "dev1",
		Type:     "switch",
		Name:     "Party Switch",
		State:    domain.Switch{Power: true},
	}); err != nil {
		t.Fatal(err)
	}

	partyHash := scriptstore.HashInstance("PartyTime", "target_lights")
	waitForInstance(t, store, partyHash, func(inst scriptstore.Instance) bool {
		return inst.Name == "PartyTime" && inst.QueryRef == "target_lights"
	})

	if err := store.Save(domain.Entity{
		ID:       "party_switch",
		Plugin:   "plugin-virtual",
		DeviceID: "dev1",
		Type:     "switch",
		Name:     "Party Switch",
		State:    domain.Switch{Power: false},
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := store.Get(scriptstore.InstanceKey{Hash: partyHash}); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for script instance %s to stop", partyHash)
}

func TestStopAllScriptsStopsOnlyOverlappingInstances(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := storage.EnsureQueryLayout(store); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveQueryDefinition(store, "basement_lights", storage.Query{
		Pattern: "plugin.dev1.>",
	}); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveQueryDefinition(store, "upstairs_lights", storage.Query{
		Pattern: "plugin.dev2.>",
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.Save(domain.Entity{
		ID:       "light1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "light",
		Name:     "Basement Lamp",
		State:    domain.Light{Power: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(domain.Entity{
		ID:       "light1",
		Plugin:   "plugin",
		DeviceID: "dev2",
		Type:     "light",
		Name:     "Upstairs Lamp",
		State:    domain.Light{Power: true},
	}); err != nil {
		t.Fatal(err)
	}

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Automation("party_time", {
		trigger = Interval(0.05),
		targets = None()
	}, function(ctx)
	end)`
	if err := saveDefinition(t, store, "party_time", source); err != nil {
		t.Fatal(err)
	}

	basementHash, err := engine.StartScript("party_time", "basement_lights")
	if err != nil {
		t.Fatal(err)
	}
	upstairsHash, err := engine.StartScript("party_time", "upstairs_lights")
	if err != nil {
		t.Fatal(err)
	}

	waitForInstance(t, store, basementHash, func(inst scriptstore.Instance) bool { return inst.Hash == basementHash })
	waitForInstance(t, store, upstairsHash, func(inst scriptstore.Instance) bool { return inst.Hash == upstairsHash })

	if err := engine.StopAllScripts("basement_lights"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, basementErr := store.Get(scriptstore.InstanceKey{Hash: basementHash})
		_, upstairsErr := store.Get(scriptstore.InstanceKey{Hash: upstairsHash})
		if basementErr != nil && upstairsErr == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected basement script stopped and upstairs script still running")
}

func waitForInstance(t *testing.T, store storage.Storage, hash string, pred func(scriptstore.Instance) bool) scriptstore.Instance {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := store.Search("sb-script.instances.*")
		if err == nil {
			for _, entry := range entries {
				var inst scriptstore.Instance
				if err := json.Unmarshal(entry.Data, &inst); err == nil && inst.Hash == hash && pred(inst) {
					return inst
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for instance %s metadata", hash)
	return scriptstore.Instance{}
}

func saveDefinition(t *testing.T, store storage.Storage, name, source string) error {
	t.Helper()
	return store.Save(scriptstore.Definition{
		Type:     "script",
		Language: "lua",
		Name:     name,
		Source:   source,
	})
}

func saveAutomation(t *testing.T, store storage.Storage, name, source string) error {
	t.Helper()
	return store.Save(scriptstore.Definition{
		Type:     "automation",
		Language: "lua",
		Name:     name,
		Source:   source,
	})
}

func waitForDecisionLabel(t *testing.T, logger logging.Store, entityKey, label string) logging.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, err := logger.List(context.Background(), logging.ListRequest{
			Source: "sb-script",
			Kind:   "automation.decision",
			Limit:  100,
		})
		if err == nil {
			for _, event := range events {
				if event.Data["label"] == label {
					return event
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for automation.decision label %q", label)
	return logging.Event{}
}

func assertDecisionLabelAbsent(t *testing.T, logger logging.Store, label string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		events, err := logger.List(context.Background(), logging.ListRequest{
			Source: "sb-script",
			Kind:   "automation.decision",
			Limit:  100,
		})
		if err == nil {
			for _, event := range events {
				if event.Data["label"] == label {
					t.Fatalf("unexpected automation.decision label %q recorded", label)
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertDecisionLabelAbsentAfter(t *testing.T, logger logging.Store, label string, beforeCount int, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		events, err := logger.List(context.Background(), logging.ListRequest{
			Source: "sb-script",
			Kind:   "automation.decision",
			Limit:  200,
		})
		if err == nil {
			if len(events) < beforeCount {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			for _, event := range events[beforeCount:] {
				if event.Data["label"] == label {
					t.Fatalf("unexpected new automation.decision label %q recorded", label)
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestScriptPrimitive_RunsOnceWhenStarted(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Save(domain.Entity{
		ID:       "light1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "light",
		Name:     "Lamp",
		State:    domain.Light{Power: true},
	}); err != nil {
		t.Fatal(err)
	}

	var called int32
	if _, err := msg.Subscribe("plugin.dev1.light1.command.light_turn_on", func(m *messenger.Message) {
		atomic.AddInt32(&called, 1)
	}); err != nil {
		t.Fatal(err)
	}

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Script("Flasher", function(ctx)
		local e = ctx.queryOne("plugin.dev1.light1")
		ctx.send(e, "light_turn_on", {})
	end)`
	if err := saveDefinition(t, store, "Flasher", source); err != nil {
		t.Fatal(err)
	}

	hash, err := engine.StartScript("Flasher", "")
	if err != nil {
		t.Fatal(err)
	}

	waitForInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.FireCount > 0
	})

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&called) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&called) == 0 {
		t.Fatal("expected Script fn to have sent a command")
	}
}

func TestScriptPrimitive_CtxAfterWorksWithoutInterval(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Save(domain.Entity{
		ID:       "light1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "light",
		Name:     "Lamp",
		State:    domain.Light{Power: true},
	}); err != nil {
		t.Fatal(err)
	}

	var callCount int32
	if _, err := msg.Subscribe("plugin.dev1.light1.command.light_turn_on", func(m *messenger.Message) {
		atomic.AddInt32(&callCount, 1)
	}); err != nil {
		t.Fatal(err)
	}

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Script("Sequencer", function(ctx)
		local e = ctx.queryOne("plugin.dev1.light1")
		ctx.send(e, "light_turn_on", {})
		ctx.after(0.05, function(child)
			local e2 = child.queryOne("plugin.dev1.light1")
			child.send(e2, "light_turn_on", {})
		end)
	end)`
	if err := saveDefinition(t, store, "Sequencer", source); err != nil {
		t.Fatal(err)
	}

	_, err = engine.StartScript("Sequencer", "")
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&callCount) >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected 2 commands via ctx.after, got %d", atomic.LoadInt32(&callCount))
}

func TestAutomationDefinition_AutoStartsOnEngineNew(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "switch",
		Name:     "Switch",
		State:    domain.Switch{Power: false},
	}); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveQueryDefinition(store, "watch_switch", storage.Query{Pattern: "plugin.dev1.switch1"}); err != nil {
		t.Fatal(err)
	}

	// Save an automation definition before creating the engine.
	source := `Automation("AutoWatcher", {
		trigger = QueryRef("watch_switch"),
		targets = None()
	}, function(ctx)
	end)`
	if err := saveAutomation(t, store, "AutoWatcher", source); err != nil {
		t.Fatal(err)
	}

	// Engine created after definitions are in storage — automations should auto-start.
	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	hash := scriptstore.HashInstance("AutoWatcher", "")
	waitForInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.Name == "AutoWatcher" && inst.Status == "running"
	})
}

func TestScriptDefinition_DoesNotAutoStart(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	source := `Script("OneShot", function(ctx)
	end)`
	if err := saveDefinition(t, store, "OneShot", source); err != nil {
		t.Fatal(err)
	}

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	time.Sleep(100 * time.Millisecond)

	hash := scriptstore.HashInstance("OneShot", "")
	entries, _ := store.Search("sb-script.instances.*")
	for _, entry := range entries {
		var inst scriptstore.Instance
		if json.Unmarshal(entry.Data, &inst) == nil && inst.Hash == hash {
			t.Fatal("Script definition should not auto-start, but found a running instance")
		}
	}
}

func TestAutomationDefinition_SavedAfterEngineStartDoesNotHotReload(t *testing.T) {
	t.Skip("reference for pre-hot-reload behavior")

	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	svc, err := logserver.New(logcfg.Config{Target: "memory"})
	if err != nil {
		t.Fatalf("server.New(memory): %v", err)
	}
	logger := svc.Store()

	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "binary_sensor",
		Name:     "Switch",
		State:    domain.BinarySensor{On: false},
	}); err != nil {
		t.Fatal(err)
	}

	engine, err := NewWithLogger(msg, store, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Automation("LateBoundAutomation", {
		trigger = Entity("plugin.dev1.switch1"),
		targets = None()
	}, function(ctx)
		ctx.decision("late_bound_fired", { trigger_on = ctx.trigger.entity.state.on })
	end)`
	if err := saveAutomation(t, store, "LateBoundAutomation", source); err != nil {
		t.Fatal(err)
	}

	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "binary_sensor",
		Name:     "Switch",
		State:    domain.BinarySensor{On: true},
	}); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)

	hash := scriptstore.HashInstance("LateBoundAutomation", "")
	entries, err := store.Search("sb-script.instances.*")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		var inst scriptstore.Instance
		if json.Unmarshal(entry.Data, &inst) == nil && inst.Hash == hash {
			t.Fatal("automation saved after engine start should not hot-reload, but found a running instance")
		}
	}

	events, err := logger.List(context.Background(), logging.ListRequest{
		Source: "sb-script",
		Kind:   "automation.triggered",
		Limit:  20,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Data["name"] == "LateBoundAutomation" {
			t.Fatal("automation saved after engine start should not hot-reload, but trigger log was recorded")
		}
	}
}

func TestAutomationDefinition_SavedAfterEngineStart_HotReloadsExpected(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	logSvc, err := logserver.New(logcfg.Config{Target: "memory"})
	if err != nil {
		t.Fatalf("log server: %v", err)
	}
	logger := logSvc.Store()

	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "binary_sensor",
		Name:     "Switch",
		State:    domain.BinarySensor{On: false},
	}); err != nil {
		t.Fatal(err)
	}

	engine, err := NewWithLogger(msg, store, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Automation("LateBoundAutomationHot", {
		trigger = Entity("plugin.dev1.switch1"),
		targets = None()
	}, function(ctx)
		ctx.decision("late_bound_hot_add", { trigger_on = ctx.trigger.entity.state.on })
	end)`
	if err := saveAutomation(t, store, "LateBoundAutomationHot", source); err != nil {
		t.Fatal(err)
	}

	hash := scriptstore.HashInstance("LateBoundAutomationHot", "")
	waitForInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.Name == "LateBoundAutomationHot" && inst.Status == "running"
	})

	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "binary_sensor",
		Name:     "Switch",
		State:    domain.BinarySensor{On: true},
	}); err != nil {
		t.Fatal(err)
	}

	waitForInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.Name == "LateBoundAutomationHot" && inst.Status == "running" && inst.FireCount > 0
	})

	waitForDecisionLabel(t, logger, "plugin.dev1.switch1", "late_bound_hot_add")
}

func TestAutomationDefinition_UpdatedAfterEngineStart_HotReloadsExpected(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	logSvc, err := logserver.New(logcfg.Config{Target: "memory"})
	if err != nil {
		t.Fatalf("log server: %v", err)
	}
	logger := logSvc.Store()

	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "binary_sensor",
		Name:     "Switch",
		State:    domain.BinarySensor{On: false},
	}); err != nil {
		t.Fatal(err)
	}

	v1 := `Automation("HotUpdateAutomation", {
		trigger = Entity("plugin.dev1.switch1"),
		targets = None()
	}, function(ctx)
		if ctx.trigger.entity.state.on then
			ctx.decision("hot_update_v1", { trigger_on = true })
		end
	end)`
	if err := saveAutomation(t, store, "HotUpdateAutomation", v1); err != nil {
		t.Fatal(err)
	}

	engine, err := NewWithLogger(msg, store, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "binary_sensor",
		Name:     "Switch",
		State:    domain.BinarySensor{On: true},
	}); err != nil {
		t.Fatal(err)
	}
	waitForDecisionLabel(t, logger, "plugin.dev1.switch1", "hot_update_v1")

	v2 := `Automation("HotUpdateAutomation", {
		trigger = Entity("plugin.dev1.switch1"),
		targets = None()
	}, function(ctx)
		if ctx.trigger.entity.state.on then
			ctx.decision("hot_update_v2", { trigger_on = true })
		end
	end)`
	if err := saveAutomation(t, store, "HotUpdateAutomation", v2); err != nil {
		t.Fatal(err)
	}
	waitForInstance(t, store, scriptstore.HashInstance("HotUpdateAutomation", ""), func(inst scriptstore.Instance) bool {
		return inst.Name == "HotUpdateAutomation" && inst.Status == "running"
	})

	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "binary_sensor",
		Name:     "Switch",
		State:    domain.BinarySensor{On: false},
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "binary_sensor",
		Name:     "Switch",
		State:    domain.BinarySensor{On: true},
	}); err != nil {
		t.Fatal(err)
	}

	waitForDecisionLabel(t, logger, "plugin.dev1.switch1", "hot_update_v2")
}

func TestAutomationDefinition_DeletedAfterEngineStart_HotReloadsExpected(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	logSvc, err := logserver.New(logcfg.Config{Target: "memory"})
	if err != nil {
		t.Fatalf("log server: %v", err)
	}
	logger := logSvc.Store()

	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "binary_sensor",
		Name:     "Switch",
		State:    domain.BinarySensor{On: false},
	}); err != nil {
		t.Fatal(err)
	}

	source := `Automation("HotDeleteAutomation", {
		trigger = Entity("plugin.dev1.switch1"),
		targets = None()
	}, function(ctx)
		if ctx.trigger.entity.state.on then
			ctx.decision("hot_delete_live", { trigger_on = true })
		end
	end)`
	if err := saveAutomation(t, store, "HotDeleteAutomation", source); err != nil {
		t.Fatal(err)
	}

	engine, err := NewWithLogger(msg, store, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "binary_sensor",
		Name:     "Switch",
		State:    domain.BinarySensor{On: false},
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "binary_sensor",
		Name:     "Switch",
		State:    domain.BinarySensor{On: true},
	}); err != nil {
		t.Fatal(err)
	}
	waitForDecisionLabel(t, logger, "plugin.dev1.switch1", "hot_delete_live")

	beforeEvents, err := logger.List(context.Background(), logging.ListRequest{
		Source: "sb-script",
		Kind:   "automation.decision",
		Limit:  100,
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeCount := len(beforeEvents)

	if err := store.Delete(scriptstore.DefinitionKey{Name: "HotDeleteAutomation"}); err != nil {
		t.Fatal(err)
	}

	hash := scriptstore.HashInstance("HotDeleteAutomation", "")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, err := store.Get(scriptstore.InstanceKey{Hash: hash})
		if err != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)

	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "binary_sensor",
		Name:     "Switch",
		State:    domain.BinarySensor{On: true},
	}); err != nil {
		t.Fatal(err)
	}

	assertDecisionLabelAbsentAfter(t, logger, "hot_delete_live", beforeCount, 300*time.Millisecond)
}

func TestQueryTrigger_FiresForPreExistingEntity(t *testing.T) {
	// Scenario: a switch is already ON when an automation that watches
	// powered-on switches is started. The automation should fire immediately
	// for the pre-existing entity without waiting for another state change.
	//
	// This fails with the current implementation because storage.Watch only
	// fires OnAdd/OnUpdate for events that arrive AFTER the subscription is
	// created. Entities already in storage are invisible to it.
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Entity exists BEFORE the automation is started.
	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "switch",
		Name:     "Switch",
		State:    domain.Switch{Power: true},
	}); err != nil {
		t.Fatal(err)
	}

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Automation("PreExistTrigger", {
trigger = Query({
where = {
{ field = "type", op = "eq", value = "switch" },
{ field = "state.power", op = "eq", value = true }
}
}),
targets = None()
}, function(ctx)
end)`
	if err := saveDefinition(t, store, "PreExistTrigger", source); err != nil {
		t.Fatal(err)
	}

	hash, err := engine.StartScript("PreExistTrigger", "")
	if err != nil {
		t.Fatal(err)
	}

	// Expect the automation to fire for the pre-existing entity without
	// any state change being published on the bus.
	waitForInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.FireCount > 0
	})
}

func TestQueryRefTrigger_FiresForPreExistingEntity(t *testing.T) {
	// Same scenario as above but using QueryRef instead of inline Query.
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Save(domain.Entity{
		ID:       "switch1",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "switch",
		Name:     "Switch",
		State:    domain.Switch{Power: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveQueryDefinition(store, "live_switches", storage.Query{
		Where: []storage.Filter{
			{Field: "type", Op: storage.Eq, Value: "switch"},
			{Field: "state.power", Op: storage.Eq, Value: true},
		},
	}); err != nil {
		t.Fatal(err)
	}

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Automation("PreExistRefTrigger", {
trigger = QueryRef("live_switches"),
targets = None()
}, function(ctx)
end)`
	if err := saveDefinition(t, store, "PreExistRefTrigger", source); err != nil {
		t.Fatal(err)
	}

	hash, err := engine.StartScript("PreExistRefTrigger", "")
	if err != nil {
		t.Fatal(err)
	}

	waitForInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.FireCount > 0
	})
}

// ==========================================================================
// ctx.send command validation
//
// ctx.send(entity, action, params) must validate the action name against the
// domain registry before publishing. Unregistered actions must not reach the
// bus — scripts calling typo'd or fabricated action names should surface an
// error rather than silently pollute NATS.
// ==========================================================================

// TestCtxSend_UnknownActionNotPublished proves that calling ctx.send with an
// action name not in the domain registry publishes nothing to NATS.
// Today, the raw subject is built and published regardless of registration.
func TestCtxSend_UnknownActionNotPublished(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Save(domain.Entity{
		ID: "light1", Plugin: "plugin", DeviceID: "dev1",
		Type: "light", Name: "Lamp", State: domain.Light{},
	}); err != nil {
		t.Fatal(err)
	}

	published := make(chan struct{}, 1)
	if _, err := msg.Subscribe("plugin.dev1.light1.command.not_a_real_action", func(m *messenger.Message) {
		published <- struct{}{}
	}); err != nil {
		t.Fatal(err)
	}
	if err := msg.Flush(); err != nil {
		t.Fatal(err)
	}

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Script("BadSend", function(ctx)
local e = ctx.queryOne("plugin.dev1.light1")
ctx.send(e, "not_a_real_action", {})
end)`
	if err := saveDefinition(t, store, "BadSend", source); err != nil {
		t.Fatal(err)
	}

	hash, err := engine.StartScript("BadSend", "")
	if err != nil {
		t.Fatal(err)
	}

	waitForInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.FireCount > 0
	})

	select {
	case <-published:
		t.Fatal("unknown action was published to NATS — should have been rejected")
	case <-time.After(300 * time.Millisecond):
		// correct: nothing published
	}
}

// TestCtxSend_KnownActionDelivered proves that ctx.send with a registered
// action still delivers to the bus after the validation is added.
func TestCtxSend_KnownActionDelivered(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Save(domain.Entity{
		ID: "light1", Plugin: "plugin", DeviceID: "dev1",
		Type: "light", Name: "Lamp", State: domain.Light{},
	}); err != nil {
		t.Fatal(err)
	}

	published := make(chan []byte, 1)
	if _, err := msg.Subscribe("plugin.dev1.light1.command.light_set_brightness", func(m *messenger.Message) {
		published <- m.Data
	}); err != nil {
		t.Fatal(err)
	}
	if err := msg.Flush(); err != nil {
		t.Fatal(err)
	}

	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Script("GoodSend", function(ctx)
local e = ctx.queryOne("plugin.dev1.light1")
ctx.send(e, "light_set_brightness", {brightness=150})
end)`
	if err := saveDefinition(t, store, "GoodSend", source); err != nil {
		t.Fatal(err)
	}

	hash, err := engine.StartScript("GoodSend", "")
	if err != nil {
		t.Fatal(err)
	}

	waitForInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.FireCount > 0
	})

	select {
	case data := <-published:
		var cmd domain.LightSetBrightness
		if err := json.Unmarshal(data, &cmd); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if cmd.Brightness != 150 {
			t.Fatalf("brightness: got %d want 150", cmd.Brightness)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for command")
	}
}

func TestCtxDecision_AppendsStructuredDecisionLog(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	logSvc, err := logserver.New(logcfg.Config{Target: "memory"})
	if err != nil {
		t.Fatalf("log server: %v", err)
	}
	logger := logSvc.Store()

	engine, err := NewWithLogger(msg, store, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Script("DecisionLog", function(ctx)
ctx.decision("ignored_falling_edge", {trigger_on=false, reason="button pulse complete"})
end)`
	if err := saveDefinition(t, store, "DecisionLog", source); err != nil {
		t.Fatal(err)
	}

	hash, err := engine.StartScript("DecisionLog", "")
	if err != nil {
		t.Fatal(err)
	}

	waitForInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.FireCount > 0
	})

	var events []logging.Event
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, err = logger.List(context.Background(), logging.ListRequest{
			Source: "sb-script",
			Kind:   "automation.decision",
			Limit:  10,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(events) != 1 {
		allEvents, _ := logger.List(context.Background(), logging.ListRequest{Source: "sb-script", Limit: 20})
		for _, event := range allEvents {
			raw, _ := json.Marshal(event)
			t.Logf("event: %s", raw)
		}
		t.Fatalf("decision log count: got %d want 1", len(events))
	}
	if got := events[0].Data["label"]; got != "ignored_falling_edge" {
		t.Fatalf("decision label: got %v want %q", got, "ignored_falling_edge")
	}
	if got := events[0].Data["reason"]; got != "button pulse complete" {
		t.Fatalf("decision reason: got %v want %q", got, "button pulse complete")
	}
	if got := events[0].Data["trigger_on"]; got != false {
		t.Fatalf("decision trigger_on: got %v want false", got)
	}
}
