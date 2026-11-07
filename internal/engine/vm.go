// Package engine manages Lua VM lifecycle for sb-script.
package engine

import (
	"sync"
	"sync/atomic"
	"time"
)

type timer interface {
	Stop() bool
}

type clock interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) timer
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) AfterFunc(d time.Duration, fn func()) timer {
	return time.AfterFunc(d, fn)
}

// vm is the base goroutine-safe VM wrapper.
// One goroutine owns the work queue; all Lua execution happens there.
type vm struct {
	queue chan func()
	done  chan struct{}
	once  sync.Once
	wg    sync.WaitGroup
}

func newVM() *vm {
	v := &vm{
		queue: make(chan func(), 64),
		done:  make(chan struct{}),
	}
	v.wg.Add(1)
	go func() {
		defer v.wg.Done()
		v.run()
	}()
	return v
}

func (v *vm) run() {
	for {
		select {
		case fn := <-v.queue:
			fn()
		case <-v.done:
			return
		}
	}
}

// enqueue schedules fn on the VM's goroutine. Non-blocking: drops if stopped.
func (v *vm) enqueue(fn func()) {
	select {
	case v.queue <- fn:
	case <-v.done:
	}
}

// stop shuts down the work goroutine and waits for it to fully exit.
// Callers can safely close the LState after stop returns.
func (v *vm) stop() {
	v.once.Do(func() { close(v.done) })
	v.wg.Wait()
}

// timerSet manages a set of cancellable timers. A timer's ID is reserved before
// the timer starts, so its callback can refer to it without a race even when it
// fires at once. A repeating timer keeps one ID for its whole life.
type timerSet struct {
	mu        sync.Mutex
	timers    map[int64]timer
	fired     map[int64]bool // one-shot timers that fired before set
	cancelled map[int64]bool // repeating timers must not start again
	counter   int64
}

func newTimerSet() *timerSet {
	return &timerSet{timers: make(map[int64]timer), fired: make(map[int64]bool), cancelled: make(map[int64]bool)}
}

// reserve returns a new timer ID.
func (ts *timerSet) reserve() int64 {
	return atomic.AddInt64(&ts.counter, 1)
}

// set records the current timer for an ID, replacing any earlier one. A timer
// for a cancelled ID is stopped instead, and one that already fired is dropped.
func (ts *timerSet) set(id int64, t timer) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.cancelled[id] {
		t.Stop()
		return
	}
	if ts.fired[id] {
		delete(ts.fired, id)
		return
	}
	ts.timers[id] = t
}

// add reserves an ID for a timer that is already running.
func (ts *timerSet) add(t timer) int64 {
	id := ts.reserve()
	ts.set(id, t)
	return id
}

// done forgets a one-shot timer that has fired.
func (ts *timerSet) done(id int64) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if _, ok := ts.timers[id]; ok {
		delete(ts.timers, id)
		return
	}
	ts.fired[id] = true
}

// cancel stops a timer, and stops a repeating one from starting again.
func (ts *timerSet) cancel(id int64) bool {
	ts.mu.Lock()
	t, ok := ts.timers[id]
	delete(ts.timers, id)
	if id > 0 && id <= atomic.LoadInt64(&ts.counter) {
		ts.cancelled[id] = true
	}
	ts.mu.Unlock()
	if ok {
		return t.Stop()
	}
	return false
}

func (ts *timerSet) cancelAll() {
	ts.mu.Lock()
	for id, t := range ts.timers {
		t.Stop()
		ts.cancelled[id] = true
	}
	ts.timers = make(map[int64]timer)
	ts.mu.Unlock()
}
