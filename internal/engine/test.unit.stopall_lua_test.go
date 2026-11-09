package engine

import (
	"testing"
	"time"

	domain "github.com/slidebolt/sb-domain"
)

func TestCtxScriptsStopAllWithColonCall(t *testing.T) {
	f := newEntityFixture(t)
	if err := saveDefinition(t, f.store, "Party", partySource); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Save(domain.Entity{ID: "btn", Plugin: "plugin-intent", DeviceID: "intents", Type: "button", Name: "b", State: domain.Button{Presses: 0}}); err != nil {
		t.Fatal(err)
	}
	src := `Automation("Off", { trigger = Entity("plugin-intent.intents.btn"), targets = None() }, function(ctx)
		ctx.scripts:stopAll("both")
	end)`
	if err := saveAutomation(t, f.store, "Off", src); err != nil {
		t.Fatal(err)
	}
	f.start()
	f.send("sb-script.script-party.scope_a", "switch_turn_on")
	f.waitPower("sb-script.script-party.scope_a", true)
	time.Sleep(300 * time.Millisecond)
	if err := f.store.Save(domain.Entity{ID: "btn", Plugin: "plugin-intent", DeviceID: "intents", Type: "button", Name: "b", State: domain.Button{Presses: 1}}); err != nil {
		t.Fatal(err)
	}
	f.waitPower("sb-script.script-party.scope_a", false)
}
