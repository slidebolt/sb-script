// Package engine manages Lua VM lifecycle for sb-script.
package engine

import (
	"sync"
	"sync/atomic"
	"time"
)

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

// timerSet manages a set of cancellable timers.
type timerSet struct {
	mu      sync.Mutex
	timers  map[int64]*time.Timer
	counter int64
}

func newTimerSet() *timerSet {
	return &timerSet{timers: make(map[int64]*time.Timer)}
}

func (ts *timerSet) add(t *time.Timer) int64 {
	id := atomic.AddInt64(&ts.counter, 1)
	ts.mu.Lock()
	ts.timers[id] = t
	ts.mu.Unlock()
	return id
}

func (ts *timerSet) cancel(id int64) bool {
	ts.mu.Lock()
	t, ok := ts.timers[id]
	if ok {
		delete(ts.timers, id)
	}
	ts.mu.Unlock()
	if ok {
		return t.Stop()
	}
	return false
}

func (ts *timerSet) cancelAll() {
	ts.mu.Lock()
	for _, t := range ts.timers {
		t.Stop()
	}
	ts.timers = make(map[int64]*time.Timer)
	ts.mu.Unlock()
}
