package app

import (
	"encoding/json"
	"testing"
	"time"

	managersdk "github.com/slidebolt/sb-manager-sdk"
	scriptstore "github.com/slidebolt/sb-script/internal/store"
)

func TestOnStart_StartUsesStorageBackedDefinitions(t *testing.T) {
	env := managersdk.NewTestEnv(t)
	env.Start("messenger")
	env.Start("storage")

	deps := map[string]json.RawMessage{
		"messenger": env.MessengerPayload(),
	}

	app := New()
	if _, err := app.OnStart(deps); err != nil {
		t.Fatalf("OnStart: %v", err)
	}
	t.Cleanup(func() { _ = app.OnShutdown() })

	def := scriptstore.Definition{
		Type:     "script",
		Language: "lua",
		Name:     "party_time",
		Source:   `Automation("party_time", { trigger = Interval(1), targets = None() }, function(ctx) end)`,
	}
	if err := env.Storage().Save(def); err != nil {
		t.Fatalf("save definition via storage: %v", err)
	}

	resp, err := env.Messenger().Request("script.start", mustJSON(t, map[string]string{
		"name": "party_time",
	}), time.Second)
	if err != nil {
		t.Fatalf("start request: %v", err)
	}
	var apiResp struct {
		OK    bool   `json:"ok"`
		Hash  string `json:"hash"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(resp.Data, &apiResp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !apiResp.OK || apiResp.Hash == "" {
		t.Fatalf("start response: %s", resp.Data)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
