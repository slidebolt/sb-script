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
	lua "github.com/yuin/gopher-lua"
)

func TestStartScript_CronTriggerPersistsNextFireAt(t *testing.T) {
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

	source := `Automation("DeckLightsOn", {
		trigger = Cron("0 19 * * *"),
		targets = None()
	}, function(ctx)
	end)`
	if err := saveAutomation(t, store, "DeckLightsOn", source); err != nil {
		t.Fatal(err)
	}

	fc := newFakeClock(time.Date(2026, 4, 12, 18, 59, 50, 0, time.UTC))
	engine, err := newEngine(msg, store, nil, fc)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	hash := scriptstore.HashInstance("DeckLightsOn", "")
	inst := waitForCronInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.Trigger.Kind == "cron" && inst.NextFireAt != nil
	})
	if inst.Trigger.Kind != "cron" {
		t.Fatalf("trigger kind: got %q want cron", inst.Trigger.Kind)
	}
	if inst.Trigger.Expr != "0 19 * * *" {
		t.Fatalf("trigger expr: got %q want %q", inst.Trigger.Expr, "0 19 * * *")
	}
	want := time.Date(2026, 4, 12, 19, 0, 0, 0, time.UTC)
	if inst.NextFireAt == nil || !inst.NextFireAt.Equal(want) {
		t.Fatalf("next fire: got %v want %v", inst.NextFireAt, want)
	}
}

func TestCronTrigger_FiresAndPublishesCommands(t *testing.T) {
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

	for _, entity := range []domain.Entity{
		{
			ID:       "outlet-00",
			Plugin:   "plugin-kasa",
			DeviceID: "kasa-2887ba950a49",
			Type:     "kasa_switch",
			Name:     "deck lights",
			Commands: []string{"switch_turn_on", "switch_turn_off", "switch_toggle"},
			State:    domain.Switch{Power: false},
		},
		{
			ID:       "outlet-01",
			Plugin:   "plugin-kasa",
			DeviceID: "kasa-2887ba950a49",
			Type:     "kasa_switch",
			Name:     "deck holiday lights",
			Commands: []string{"switch_turn_on", "switch_turn_off", "switch_toggle"},
			State:    domain.Switch{Power: false},
		},
	} {
		if err := store.Save(entity); err != nil {
			t.Fatal(err)
		}
	}

	commandCh := make(chan string, 4)
	for _, subject := range []string{
		"plugin-kasa.kasa-2887ba950a49.outlet-00.command.switch_turn_on",
		"plugin-kasa.kasa-2887ba950a49.outlet-01.command.switch_turn_on",
	} {
		subject := subject
		if _, err := msg.Subscribe(subject, func(m *messenger.Message) {
			commandCh <- subject
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := msg.Flush(); err != nil {
		t.Fatal(err)
	}

	source := `Automation("DeckLightsOn", {
		trigger = Cron("0 19 * * *"),
		targets = Query("plugin-kasa.kasa-2887ba950a49.outlet-*")
	}, function(ctx)
		ctx.targets:each(function(e)
			ctx.send(e, "switch_turn_on", {})
		end)
	end)`
	if err := saveAutomation(t, store, "DeckLightsOn", source); err != nil {
		t.Fatal(err)
	}

	fc := newFakeClock(time.Date(2026, 4, 12, 18, 59, 50, 0, time.UTC))
	engine, err := newEngine(msg, store, nil, fc)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	hash := scriptstore.HashInstance("DeckLightsOn", "")

	fc.Advance(10 * time.Second)

	gotSubjects := map[string]bool{}
	deadline := time.Now().Add(500 * time.Millisecond)
	for len(gotSubjects) < 2 && time.Now().Before(deadline) {
		select {
		case subject := <-commandCh:
			gotSubjects[subject] = true
		case <-time.After(10 * time.Millisecond):
		}
	}
	if len(gotSubjects) != 2 {
		t.Fatalf("published subjects = %v, want 2 outlet commands", gotSubjects)
	}

	inst := waitForCronInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.FireCount == 1 && inst.NextFireAt != nil
	})
	if inst.FireCount != 1 {
		t.Fatalf("fire count: got %d want 1", inst.FireCount)
	}
	wantNext := time.Date(2026, 4, 13, 19, 0, 0, 0, time.UTC)
	if inst.NextFireAt == nil || !inst.NextFireAt.Equal(wantNext) {
		t.Fatalf("next fire after run: got %v want %v", inst.NextFireAt, wantNext)
	}
}

func TestAutomationDefinition_SavedAfterEngineStart_CronHotReloadsExpected(t *testing.T) {
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

	fc := newFakeClock(time.Date(2026, 4, 12, 18, 59, 50, 0, time.UTC))
	engine, err := newEngine(msg, store, nil, fc)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	source := `Automation("LateBoundCronHot", {
		trigger = Cron("0 19 * * *"),
		targets = None()
	}, function(ctx)
	end)`
	if err := saveAutomation(t, store, "LateBoundCronHot", source); err != nil {
		t.Fatal(err)
	}

	hash := scriptstore.HashInstance("LateBoundCronHot", "")
	inst := waitForCronInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.Name == "LateBoundCronHot" && inst.Trigger.Expr == "0 19 * * *" && inst.NextFireAt != nil
	})
	want := time.Date(2026, 4, 12, 19, 0, 0, 0, time.UTC)
	if inst.NextFireAt == nil || !inst.NextFireAt.Equal(want) {
		t.Fatalf("next fire after hot-add: got %v want %v", inst.NextFireAt, want)
	}
}

func TestAutomationDefinition_UpdatedAfterEngineStart_CronReschedulesExpected(t *testing.T) {
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

	v1 := `Automation("HotUpdateCron", {
		trigger = Cron("0 19 * * *"),
		targets = None()
	}, function(ctx)
	end)`
	if err := saveAutomation(t, store, "HotUpdateCron", v1); err != nil {
		t.Fatal(err)
	}

	fc := newFakeClock(time.Date(2026, 4, 12, 18, 59, 50, 0, time.UTC))
	engine, err := newEngine(msg, store, nil, fc)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	hash := scriptstore.HashInstance("HotUpdateCron", "")
	initial := waitForCronInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.Trigger.Expr == "0 19 * * *" && inst.NextFireAt != nil
	})
	wantInitial := time.Date(2026, 4, 12, 19, 0, 0, 0, time.UTC)
	if initial.NextFireAt == nil || !initial.NextFireAt.Equal(wantInitial) {
		t.Fatalf("initial next fire: got %v want %v", initial.NextFireAt, wantInitial)
	}

	v2 := `Automation("HotUpdateCron", {
		trigger = Cron("5 19 * * *"),
		targets = None()
	}, function(ctx)
	end)`
	if err := saveAutomation(t, store, "HotUpdateCron", v2); err != nil {
		t.Fatal(err)
	}

	updated := waitForCronInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.Trigger.Expr == "5 19 * * *" && inst.NextFireAt != nil
	})
	wantUpdated := time.Date(2026, 4, 12, 19, 5, 0, 0, time.UTC)
	if updated.NextFireAt == nil || !updated.NextFireAt.Equal(wantUpdated) {
		t.Fatalf("updated next fire: got %v want %v", updated.NextFireAt, wantUpdated)
	}

	fc.Advance(10 * time.Second)
	stillWaiting := waitForCronInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.Trigger.Expr == "5 19 * * *"
	})
	if stillWaiting.FireCount != 0 {
		t.Fatalf("fire count at old schedule: got %d want 0", stillWaiting.FireCount)
	}
	if stillWaiting.NextFireAt == nil || !stillWaiting.NextFireAt.Equal(wantUpdated) {
		t.Fatalf("next fire after old schedule passed: got %v want %v", stillWaiting.NextFireAt, wantUpdated)
	}

	fc.Advance(5 * time.Minute)
	fired := waitForCronInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.FireCount == 1 && inst.NextFireAt != nil
	})
	wantNext := time.Date(2026, 4, 13, 19, 5, 0, 0, time.UTC)
	if fired.NextFireAt == nil || !fired.NextFireAt.Equal(wantNext) {
		t.Fatalf("next fire after updated run: got %v want %v", fired.NextFireAt, wantNext)
	}
}

func TestCronTrigger_UsesSingleExactTimer(t *testing.T) {
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

	source := `Automation("ExactCronTimer", {
		trigger = Cron("0 19 * * *"),
		targets = None()
	}, function(ctx)
	end)`
	if err := saveAutomation(t, store, "ExactCronTimer", source); err != nil {
		t.Fatal(err)
	}

	fc := newFakeClock(time.Date(2026, 4, 12, 18, 59, 50, 0, time.UTC))
	engine, err := newEngine(msg, store, nil, fc)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	hash := scriptstore.HashInstance("ExactCronTimer", "")
	initial := waitForCronInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.Trigger.Expr == "0 19 * * *" && inst.NextFireAt != nil
	})
	wantFirst := time.Date(2026, 4, 12, 19, 0, 0, 0, time.UTC)
	if initial.NextFireAt == nil || !initial.NextFireAt.Equal(wantFirst) {
		t.Fatalf("initial next fire: got %v want %v", initial.NextFireAt, wantFirst)
	}
	if got := fc.timerCount(); got != 1 {
		t.Fatalf("timer count after schedule: got %d want 1", got)
	}
	if when, ok := fc.nextTimerTime(); !ok || !when.Equal(wantFirst) {
		t.Fatalf("scheduled timer: got %v ok=%v want %v", when, ok, wantFirst)
	}

	fc.Advance(5 * time.Second)
	midway := waitForCronInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.Trigger.Expr == "0 19 * * *"
	})
	if midway.FireCount != 0 {
		t.Fatalf("fire count before due time: got %d want 0", midway.FireCount)
	}
	if got := fc.timerCount(); got != 1 {
		t.Fatalf("timer count before due time: got %d want 1", got)
	}
	if when, ok := fc.nextTimerTime(); !ok || !when.Equal(wantFirst) {
		t.Fatalf("scheduled timer before due time: got %v ok=%v want %v", when, ok, wantFirst)
	}

	fc.Advance(5 * time.Second)
	fired := waitForCronInstance(t, store, hash, func(inst scriptstore.Instance) bool {
		return inst.FireCount == 1 && inst.NextFireAt != nil
	})
	wantNext := time.Date(2026, 4, 13, 19, 0, 0, 0, time.UTC)
	if fired.NextFireAt == nil || !fired.NextFireAt.Equal(wantNext) {
		t.Fatalf("next fire after exact timer run: got %v want %v", fired.NextFireAt, wantNext)
	}
	if got := fc.timerCount(); got != 1 {
		t.Fatalf("timer count after reschedule: got %d want 1", got)
	}
	if when, ok := fc.nextTimerTime(); !ok || !when.Equal(wantNext) {
		t.Fatalf("scheduled timer after reschedule: got %v ok=%v want %v", when, ok, wantNext)
	}
}

func waitForCronInstance(t *testing.T, store storage.Storage, hash string, pred func(scriptstore.Instance) bool) scriptstore.Instance {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last scriptstore.Instance
	for time.Now().Before(deadline) {
		data, err := store.Get(scriptstore.InstanceKey{Hash: hash})
		if err == nil {
			var inst scriptstore.Instance
			if err := json.Unmarshal(data, &inst); err == nil {
				last = inst
				if pred(inst) {
					return inst
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for instance %s metadata; last=%+v", hash, last)
	return scriptstore.Instance{}
}

func TestParseCronTriggerSpec_WithTimezoneExpression(t *testing.T) {
	spec, err := parseTriggerSpecTable(`return { kind = "cron", expr = "CRON_TZ=America/Chicago 0 19 * * *" }`)
	if err != nil {
		t.Fatal(err)
	}
	if spec.kind != "cron" {
		t.Fatalf("kind: got %q want cron", spec.kind)
	}
	if spec.expr != "CRON_TZ=America/Chicago 0 19 * * *" {
		t.Fatalf("expr: got %q", spec.expr)
	}
	now := time.Date(2026, 4, 12, 23, 30, 0, 0, time.UTC)
	next, err := spec.next(now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next: got %v want %v", next, want)
	}
}

func parseTriggerSpecTable(source string) (triggerSpec, error) {
	L := newLuaVM(realClock{}).L
	defer L.Close()
	if err := L.DoString(source); err != nil {
		return triggerSpec{}, err
	}
	tbl := L.Get(-1)
	specTbl, ok := tbl.(*lua.LTable)
	if !ok {
		return triggerSpec{}, nil
	}
	return parseTriggerSpec(L, specTbl)
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*fakeTimer]struct{}
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{
		now:    now,
		timers: make(map[*fakeTimer]struct{}),
	}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, fn func()) timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{
		clock: c,
		when:  c.now.Add(d),
		fn:    fn,
	}
	c.timers[t] = struct{}{}
	return t
}

func (c *fakeClock) Advance(d time.Duration) {
	target := c.Now().Add(d)
	for {
		timer := c.nextDueTimer(target)
		if timer == nil {
			c.mu.Lock()
			c.now = target
			c.mu.Unlock()
			return
		}
		timer.fire()
	}
}

func (c *fakeClock) nextDueTimer(target time.Time) *fakeTimer {
	c.mu.Lock()
	defer c.mu.Unlock()

	var next *fakeTimer
	for timer := range c.timers {
		if timer.stopped || timer.when.After(target) {
			continue
		}
		if next == nil || timer.when.Before(next.when) {
			next = timer
		}
	}
	if next == nil {
		return nil
	}
	c.now = next.when
	delete(c.timers, next)
	next.stopped = true
	return next
}

func (c *fakeClock) timerCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for timer := range c.timers {
		if timer.stopped {
			continue
		}
		count++
	}
	return count
}

func (c *fakeClock) nextTimerTime() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var (
		next time.Time
		ok   bool
	)
	for timer := range c.timers {
		if timer.stopped {
			continue
		}
		if !ok || timer.when.Before(next) {
			next = timer.when
			ok = true
		}
	}
	return next, ok
}

type fakeTimer struct {
	clock   *fakeClock
	when    time.Time
	fn      func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped {
		return false
	}
	t.stopped = true
	delete(t.clock.timers, t)
	return true
}

func (t *fakeTimer) fire() {
	t.fn()
}
