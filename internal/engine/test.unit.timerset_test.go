package engine

import "testing"

type stubTimer struct{ stopped bool }

func (f *stubTimer) Stop() bool { f.stopped = true; return true }

func TestTimerSet_FiredBeforeSetLeavesNothingBehind(t *testing.T) {
	ts := newTimerSet()
	id := ts.reserve()
	ts.done(id) // the callback ran before set
	ts.set(id, &stubTimer{})
	if len(ts.timers) != 0 {
		t.Fatalf("a fired timer was kept: %v", ts.timers)
	}
}

func TestTimerSet_CancelledRepeatingTimerDoesNotRestart(t *testing.T) {
	ts := newTimerSet()
	id := ts.reserve()
	first := &stubTimer{}
	ts.set(id, first)
	if !ts.cancel(id) || !first.stopped {
		t.Fatal("cancel did not stop the current timer")
	}
	next := &stubTimer{} // the next tick, scheduled as cancel ran
	ts.set(id, next)
	if !next.stopped || len(ts.timers) != 0 {
		t.Fatal("a cancelled repeating timer started again")
	}
}

func TestTimerSet_RepeatingTimerKeepsItsID(t *testing.T) {
	ts := newTimerSet()
	id := ts.reserve()
	ts.set(id, &stubTimer{})
	second := &stubTimer{}
	ts.set(id, second)
	ts.cancel(id)
	if !second.stopped {
		t.Fatal("cancel by the original ID did not stop the latest tick")
	}
}

func TestTimerSet_CancelOfUnknownIDIsHarmless(t *testing.T) {
	ts := newTimerSet()
	if ts.cancel(12345) || len(ts.cancelled) != 0 {
		t.Fatal("cancelling an ID never handed out should do nothing")
	}
}
