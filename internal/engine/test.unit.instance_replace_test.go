package engine

import (
	"sync/atomic"
	"testing"
	"time"

	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	scriptstore "github.com/slidebolt/sb-script/internal/store"
	storageserver "github.com/slidebolt/sb-storage-server"
)

// Saving an automation starts it, and so can an explicit start that races with
// the save. Whichever registers second must replace the first. Before, the first
// VM was dropped from the map but kept running, invisible and unstoppable, so
// every trigger ran twice.
func TestAutomation_StartingTwiceLeavesOneRunning(t *testing.T) {
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
	for _, e := range []domain.Entity{
		{ID: "light1", Plugin: "plugin", DeviceID: "dev1", Type: "light", Name: "Lamp", State: domain.Light{}},
		{ID: "switch1", Plugin: "plugin", DeviceID: "dev1", Type: "switch", Name: "Switch", State: domain.Switch{}},
	} {
		if err := store.Save(e); err != nil {
			t.Fatal(err)
		}
	}
	var sent int32
	if _, err := msg.Subscribe("plugin.dev1.light1.command.light_turn_on", func(*messenger.Message) {
		atomic.AddInt32(&sent, 1)
	}); err != nil {
		t.Fatal(err)
	}
	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()

	src := `Automation("Follow", {
		trigger = Entity("plugin.dev1.switch1"),
		targets = None(),
	}, function(ctx)
		ctx.send(ctx.queryOne("plugin.dev1.light1"), "light_turn_on", {})
	end)`
	if err := saveAutomation(t, store, "Follow", src); err != nil {
		t.Fatal(err)
	}
	hash := scriptstore.HashInstance("Follow", "")
	deadline := time.Now().Add(5 * time.Second)
	for {
		engine.mu.RLock()
		_, running := engine.instances[hash]
		engine.mu.RUnlock()
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("saving the automation did not start it")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The racing second start.
	if err := engine.startInstance("Follow", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	if err := store.Save(domain.Entity{ID: "switch1", Plugin: "plugin", DeviceID: "dev1", Type: "switch", Name: "Switch", State: domain.Switch{Power: true}}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for atomic.LoadInt32(&sent) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond) // room for a duplicate to show up
	if n := atomic.LoadInt32(&sent); n != 1 {
		t.Fatalf("one trigger sent %d commands, want 1", n)
	}
}
