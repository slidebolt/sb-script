package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	scriptstore "github.com/slidebolt/sb-script/internal/store"
	storage "github.com/slidebolt/sb-storage-sdk"
	storageserver "github.com/slidebolt/sb-storage-server"
)

func TestServiceDoesNotExposeSaveDefinitionRPC(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { msg.Close() })

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}

	svc, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Shutdown() })

	req, err := json.Marshal(map[string]string{
		"name":   "party_time",
		"source": `Automation("PartyTime", { trigger = Interval(1), targets = None() }, function(ctx) end)`,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = msg.Request("script.save_definition", req, time.Second)
	if err == nil {
		t.Fatal("expected script.save_definition to have no responders")
	}
	if !strings.Contains(err.Error(), "no responders") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServiceStartAcceptsQueryRef(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { msg.Close() })

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Save(domain.Entity{
		ID:       "lamp",
		Plugin:   "test",
		DeviceID: "dev1",
		Type:     "light",
		Name:     "Lamp",
		State:    domain.Light{Power: true, ColorMode: "rgb"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(scriptstore.Definition{
		Type:     "script",
		Language: "lua",
		Name:     "party_time",
		Source: `Automation("party_time", {
  trigger = Interval(1),
  targets = None()
}, function(ctx) end)`,
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

	svc, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Shutdown() })

	req, err := json.Marshal(map[string]any{
		"name":     "party_time",
		"queryRef": "rgb_lights",
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := msg.Request("script.start", req, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var apiResp APIResponse
	if err := json.Unmarshal(resp.Data, &apiResp); err != nil {
		t.Fatal(err)
	}
	if !apiResp.OK || apiResp.Hash == "" {
		t.Fatalf("start response: %s", resp.Data)
	}
}

func TestServiceStartMissingScriptRespondsWithError(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { msg.Close() })

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}

	svc, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Shutdown() })

	req, err := json.Marshal(map[string]any{"name": "missing_script"})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := msg.Request("script.start", req, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	var apiResp APIResponse
	if err := json.Unmarshal(resp.Data, &apiResp); err != nil {
		t.Fatal(err)
	}
	if apiResp.OK {
		t.Fatalf("expected error response, got %s", resp.Data)
	}
	if !strings.Contains(apiResp.Error, "missing_script") {
		t.Fatalf("unexpected start error: %s", resp.Data)
	}
}

func TestServiceStopMissingScriptRespondsOK(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { msg.Close() })

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}

	svc, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Shutdown() })

	req, err := json.Marshal(map[string]any{"name": "missing_script"})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := msg.Request("script.stop", req, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	var apiResp APIResponse
	if err := json.Unmarshal(resp.Data, &apiResp); err != nil {
		t.Fatal(err)
	}
	if !apiResp.OK {
		t.Fatalf("expected ok stop response, got %s", resp.Data)
	}
}

func TestServiceStopAllAcceptsQueryRef(t *testing.T) {
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { msg.Close() })

	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatal(err)
	}

	if err := storage.EnsureQueryLayout(store); err != nil {
		t.Fatal(err)
	}
	if err := storage.SaveQueryDefinition(store, "basement_lights", storage.Query{
		Pattern: "plugin.dev1.>",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(domain.Entity{
		ID:       "lamp",
		Plugin:   "plugin",
		DeviceID: "dev1",
		Type:     "light",
		Name:     "Lamp",
		State:    domain.Light{Power: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(scriptstore.Definition{
		Type:     "script",
		Language: "lua",
		Name:     "party_time",
		Source: `Automation("party_time", {
  trigger = Interval(1),
  targets = None()
}, function(ctx) end)`,
	}); err != nil {
		t.Fatal(err)
	}

	svc, err := New(msg, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Shutdown() })

	startReq, err := json.Marshal(map[string]any{
		"name":     "party_time",
		"queryRef": "basement_lights",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := msg.Request("script.start", startReq, time.Second); err != nil {
		t.Fatal(err)
	}

	stopAllReq, err := json.Marshal(map[string]any{"queryRef": "basement_lights"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := msg.Request("script.stop_all", stopAllReq, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var apiResp APIResponse
	if err := json.Unmarshal(resp.Data, &apiResp); err != nil {
		t.Fatal(err)
	}
	if !apiResp.OK {
		t.Fatalf("stop_all response: %s", resp.Data)
	}
}
