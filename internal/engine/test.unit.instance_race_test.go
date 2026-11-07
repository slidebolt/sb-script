package engine

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	scriptstore "github.com/slidebolt/sb-script/internal/store"
	storage "github.com/slidebolt/sb-storage-sdk"
	storageserver "github.com/slidebolt/sb-storage-server"
)

// slowFirstInstanceSave delays saving an instance record that has never fired,
// widening the window in which a Script's one run can finish first.
type slowFirstInstanceSave struct {
	storage.Storage
	delayed atomic.Bool
}

func (s *slowFirstInstanceSave) Save(v storage.Keyed) error {
	if inst, ok := v.(scriptstore.Instance); ok && inst.FireCount == 0 && s.delayed.CompareAndSwap(false, true) {
		time.Sleep(300 * time.Millisecond)
	}
	return s.Storage.Save(v)
}

// The initial instance record must not overwrite the FireCount saved by the
// Script's run. This failed on arm64 CI when the run happened to finish first.
func TestScriptPrimitive_FireCountSurvivesASlowInitialSave(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	defer msg.Close()
	base, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &slowFirstInstanceSave{Storage: base}

	if err := store.Save(domain.Entity{ID: "light1", Plugin: "plugin", DeviceID: "dev1", Type: "light", Name: "Lamp", State: domain.Light{}}); err != nil {
		t.Fatal(err)
	}
	engine, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Shutdown()
	src := `Script("Once", function(ctx) ctx.send(ctx.queryOne("plugin.dev1.light1"), "light_turn_on", {}) end)`
	if err := saveDefinition(t, store, "Once", src); err != nil {
		t.Fatal(err)
	}
	hash, err := engine.StartScript("Once", "")
	if err != nil {
		t.Fatal(err)
	}
	// Give the run time to finish, then check what storage kept.
	time.Sleep(500 * time.Millisecond)
	data, err := base.Get(scriptstore.InstanceKey{Hash: hash})
	if err != nil {
		t.Fatal(err)
	}
	var inst scriptstore.Instance
	if err := json.Unmarshal(data, &inst); err != nil {
		t.Fatal(err)
	}
	if inst.FireCount != 1 || inst.LastFiredAt == nil {
		t.Fatalf("stored instance lost its run: fire count %d, last fired %v", inst.FireCount, inst.LastFiredAt)
	}
}
