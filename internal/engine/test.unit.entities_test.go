package engine

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	scriptstore "github.com/slidebolt/sb-script/internal/store"
	storage "github.com/slidebolt/sb-storage-sdk"
	storageserver "github.com/slidebolt/sb-storage-server"
)

type entityFixture struct {
	t     *testing.T
	msg   messenger.Messenger
	store storage.Storage
	eng   *Engine

	mu   sync.Mutex
	sent []string // "<entity key>.<action>" of light commands
}

func newEntityFixture(t *testing.T) *entityFixture {
	t.Helper()
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { msg.Close() })
	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := storage.EnsureQueryLayout(store); err != nil {
		t.Fatal(err)
	}
	f := &entityFixture{t: t, msg: msg, store: store}
	for _, id := range []string{"light1", "light2"} {
		if err := store.Save(domain.Entity{ID: id, Plugin: "plugin", DeviceID: "dev1", Type: "light", Name: id,
			State: domain.Light{Power: true}}); err != nil {
			t.Fatal(err)
		}
	}
	for name, pattern := range map[string]string{"scope_a": "plugin.dev1.light1", "scope_b": "plugin.dev1.light2", "both": "plugin.dev1.*"} {
		if err := storage.SaveQueryDefinition(store, name, storage.Query{Pattern: pattern}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := msg.Subscribe("plugin.dev1.*.command.*", func(m *messenger.Message) {
		f.mu.Lock()
		f.sent = append(f.sent, m.Subject)
		f.mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *entityFixture) start() {
	f.t.Helper()
	eng, err := New(f.msg, f.store)
	if err != nil {
		f.t.Fatal(err)
	}
	f.eng = eng
	f.t.Cleanup(eng.Shutdown)
}

func (f *entityFixture) power(key string) (bool, bool) {
	raw, err := f.store.Get(entityKeyOf(key))
	if err != nil {
		return false, false
	}
	var ent domain.Entity
	if err := json.Unmarshal(raw, &ent); err != nil {
		f.t.Fatalf("decode %s: %v", key, err)
	}
	sw, ok := ent.State.(domain.Switch)
	if !ok {
		f.t.Fatalf("%s state is %T, want domain.Switch", key, ent.State)
	}
	return sw.Power, true
}

func (f *entityFixture) waitPower(key string, want bool) {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, ok := f.power(key); ok && got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, ok := f.power(key)
	f.t.Fatalf("%s: power=%v exists=%v, want power=%v", key, got, ok, want)
}

func (f *entityFixture) waitGone(key string) {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := f.power(key); !ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatalf("%s still exists", key)
}

func (f *entityFixture) send(key, action string) {
	f.t.Helper()
	if err := f.msg.Publish(key+".command."+action, []byte("{}")); err != nil {
		f.t.Fatal(err)
	}
}

func (f *entityFixture) running(name, scope string) bool {
	_, ok := f.eng.running(name, scope)
	return ok
}

func (f *entityFixture) waitRunning(name, scope string, want bool) {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f.running(name, scope) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatalf("%s/%s running=%v, want %v", name, scope, !want, want)
}

func (f *entityFixture) waitSent(subject string) {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for _, s := range f.sent {
			if s == subject {
				f.mu.Unlock()
				return
			}
		}
		f.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t.Fatalf("%s not sent; sent: %v", subject, f.sent)
}

func (f *entityFixture) count(subject string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.sent {
		if s == subject {
			n++
		}
	}
	return n
}

func entityKeyOf(key string) domain.EntityKey {
	var parts [3]string
	i := 0
	start := 0
	for j := 0; j < len(key) && i < 2; j++ {
		if key[j] == '.' {
			parts[i] = key[start:j]
			i++
			start = j + 1
		}
	}
	parts[2] = key[start:]
	return domain.EntityKey{Plugin: parts[0], DeviceID: parts[1], ID: parts[2]}
}

const partySource = `Script("Party", {
	scopes = { "scope_a", "scope_b" },
	stop = function(ctx)
		ctx.targets:each(function(e) ctx.send(e, "light_set_color_temp", { mireds = 370 }) end)
		ctx.after(0.2, function(c)
			c.targets:each(function(e) c.send(e, "light_turn_off", {}) end)
		end)
	end,
}, function(ctx)
	local function tick()
		ctx.targets:each(function(e) ctx.send(e, "light_turn_on", {}) end)
		ctx.after(0.1, tick)
	end
	tick()
end)`

func TestScopedScriptSwitchesStartAndStopTheirOwnInstance(t *testing.T) {
	f := newEntityFixture(t)
	if err := saveDefinition(t, f.store, "Party", partySource); err != nil {
		t.Fatal(err)
	}
	f.start()

	a, b := "sb-script.script-party.scope_a", "sb-script.script-party.scope_b"
	f.waitPower(a, false)
	f.waitPower(b, false)

	f.send(a, "switch_turn_on")
	f.waitRunning("Party", "scope_a", true)
	f.waitPower(a, true)
	raw, _ := f.store.Get(entityKeyOf(a))
	var stored struct {
		State map[string]any `json:"state"`
	}
	_ = json.Unmarshal(raw, &stored)
	if stored.State["running"] != true || stored.State["startedAt"] == nil {
		t.Fatalf("state should record running and startedAt: %s", raw)
	}
	f.send(b, "switch_turn_on")
	f.waitRunning("Party", "scope_b", true)

	// Graceful stop of scope_a: its stop runs on light1 (and its timer finishes),
	// scope_b keeps running.
	f.send(a, "switch_turn_off")
	f.waitPower(a, false)
	f.waitSent("plugin.dev1.light1.command.light_set_color_temp")
	f.waitSent("plugin.dev1.light1.command.light_turn_off")
	if f.running("Party", "scope_a") {
		t.Fatal("scope_a still listed as running")
	}
	if !f.running("Party", "scope_b") {
		t.Fatal("scope_b stopped too")
	}
	if n := f.count("plugin.dev1.light2.command.light_set_color_temp"); n != 0 {
		t.Fatalf("stop ran on scope_b's light: %d", n)
	}
	// The script's own loop ended with the stop.
	time.Sleep(300 * time.Millisecond)
	before := f.count("plugin.dev1.light1.command.light_turn_on")
	time.Sleep(300 * time.Millisecond)
	if after := f.count("plugin.dev1.light1.command.light_turn_on"); after != before {
		t.Fatalf("scope_a loop still running: %d -> %d", before, after)
	}

	// Toggle turns scope_b off.
	f.send(b, "switch_toggle")
	f.waitPower(b, false)
	f.waitRunning("Party", "scope_b", false)
}

func TestHardStopsFlipTheSwitchOffWithoutRunningStop(t *testing.T) {
	f := newEntityFixture(t)
	if err := saveDefinition(t, f.store, "Party", partySource); err != nil {
		t.Fatal(err)
	}
	// A mode's glue automation that targets the same lights. stopAll must not
	// stop it.
	glue := `Automation("Glue", { trigger = Interval(60), targets = QueryRef("both") }, function(ctx) end)`
	if err := saveAutomation(t, f.store, "Glue", glue); err != nil {
		t.Fatal(err)
	}
	f.start()
	a := "sb-script.script-party.scope_a"

	if _, err := f.eng.StartScript("Party", "scope_a"); err != nil {
		t.Fatal(err)
	}
	f.waitPower(a, true)
	// Make Glue's resolved targets overlap, as markFired does on a fire.
	f.eng.saveInstanceRecord(scopeHash("Glue", ""), func(inst *scriptstore.Instance) {
		inst.ResolvedTargets = []string{"plugin.dev1.light1", "plugin.dev1.light2"}
	})

	if err := f.eng.StopAllScripts("scope_a"); err != nil {
		t.Fatal(err)
	}
	f.waitPower(a, false)
	if !f.running("Glue", "") {
		t.Fatal("stopAll stopped an automation")
	}
	time.Sleep(300 * time.Millisecond)
	if n := f.count("plugin.dev1.light1.command.light_set_color_temp"); n != 0 {
		t.Fatalf("hard stop ran the stop option (%d commands)", n)
	}
}

func TestUnscopedScriptSwitchFollowsSelfStop(t *testing.T) {
	f := newEntityFixture(t)
	src := `Script("Once", function(ctx)
		ctx.after(0.2, function(c) c.scripts:stop("Once") end)
	end)`
	if err := saveDefinition(t, f.store, "Once", src); err != nil {
		t.Fatal(err)
	}
	f.start()
	k := "sb-script.script-once.run"
	f.waitPower(k, false)
	f.send(k, "switch_turn_on")
	f.waitPower(k, true)
	f.waitPower(k, false)
	if f.running("Once", "") {
		t.Fatal("still running after self-stop")
	}
}

func TestAutomationDisabledSurvivesRestart(t *testing.T) {
	f := newEntityFixture(t)
	src := `Automation("Auto", { trigger = Interval(60), targets = None() }, function(ctx) end)`
	if err := saveAutomation(t, f.store, "Auto", src); err != nil {
		t.Fatal(err)
	}
	f.start()
	k := "sb-script.automation-auto.enabled"
	f.waitPower(k, true)
	f.waitRunning("Auto", "", true)

	f.send(k, "switch_turn_off")
	f.waitPower(k, false)
	f.waitRunning("Auto", "", false)

	f.eng.Shutdown()
	f.start()
	time.Sleep(200 * time.Millisecond)
	if f.running("Auto", "") {
		t.Fatal("disabled automation started after restart")
	}
	if p, _ := f.power(k); p {
		t.Fatal("enabled switch came back on after restart")
	}

	// Saving a new version keeps it disabled.
	if err := saveAutomation(t, f.store, "Auto", src+"\n-- v2"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if f.running("Auto", "") {
		t.Fatal("disabled automation started after a save")
	}

	f.send(k, "switch_turn_on")
	f.waitRunning("Auto", "", true)
	f.waitPower(k, true)
}

func TestScriptEntitiesFollowTheDefinition(t *testing.T) {
	f := newEntityFixture(t)
	if err := saveDefinition(t, f.store, "Party", partySource); err != nil {
		t.Fatal(err)
	}
	f.start()
	a, b := "sb-script.script-party.scope_a", "sb-script.script-party.scope_b"
	f.waitPower(b, false)

	// A running scope keeps running (and stays on) across a save.
	if _, err := f.eng.StartScript("Party", "scope_a"); err != nil {
		t.Fatal(err)
	}
	f.waitPower(a, true)
	oneScope := `Script("Party", { scopes = { "scope_a" } }, function(ctx) end)`
	if err := saveDefinition(t, f.store, "Party", oneScope); err != nil {
		t.Fatal(err)
	}
	f.waitGone(b)
	time.Sleep(200 * time.Millisecond)
	f.waitPower(a, true)
	if !f.running("Party", "scope_a") {
		t.Fatal("scope_a stopped by the save")
	}

	// The old two-argument form gets one unscoped switch.
	if err := saveDefinition(t, f.store, "Party", `Script("Party", function(ctx) end)`); err != nil {
		t.Fatal(err)
	}
	f.waitGone(a)
	f.waitPower("sb-script.script-party.run", false)

	if err := f.eng.DeleteDefinition("Party"); err != nil {
		t.Fatal(err)
	}
	f.waitGone("sb-script.script-party.run")
}

func TestDescribeReadsDeclarationsWithoutRunningCallbacks(t *testing.T) {
	d, err := describe("Party", `
		local n = 0
		Script("Other", { scopes = { "x" } }, function(ctx) error("ran") end)
		Script("Party", { scopes = { "a", "b", "a" }, stop = function() error("ran") end }, function(ctx) error("ran") end)`)
	if err != nil {
		t.Fatal(err)
	}
	if d.kind != "script" || len(d.scopes) != 2 || d.scopes[0] != "a" || d.scopes[1] != "b" {
		t.Fatalf("got %+v", d)
	}
	d, err = describe("Auto", `Automation("Auto", { trigger = Entity("x.y.z"), targets = QueryRef("q") }, function(ctx) error("ran") end)`)
	if err != nil || d.kind != "automation" {
		t.Fatalf("got %+v, %v", d, err)
	}
	if _, err := describe("Loop", `while true do end`); err == nil {
		t.Fatal("expected a timeout error")
	}
}

func TestStopAllCutsAGracefulFadeShort(t *testing.T) {
	f := newEntityFixture(t)
	src := `Script("Show", {
		scopes = { "scope_a" },
		stop = function(ctx)
			ctx.after(0.6, function(c)
				c.targets:each(function(e) c.send(e, "light_set_white", { white = 1 }) end)
			end)
		end,
	}, function(ctx) end)`
	if err := saveDefinition(t, f.store, "Show", src); err != nil {
		t.Fatal(err)
	}
	f.start()
	k := "sb-script.script-show.scope_a"
	f.waitPower(k, false)

	// Uninterrupted, the fade's step arrives.
	f.send(k, "switch_turn_on")
	f.waitPower(k, true)
	f.send(k, "switch_turn_off")
	f.waitSent("plugin.dev1.light1.command.light_set_white")

	// A wall switch's stopAll during the fade ends it.
	f.send(k, "switch_turn_on")
	f.waitPower(k, true)
	f.send(k, "switch_turn_off")
	f.waitPower(k, false)
	if err := f.eng.StopAllScripts("scope_a"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if n := f.count("plugin.dev1.light1.command.light_set_white"); n != 1 {
		t.Fatalf("fade step after stopAll: %d sends, want 1", n)
	}
}
