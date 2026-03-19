//go:build bdd

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	"github.com/slidebolt/sb-script/internal/engine"
	scriptstore "github.com/slidebolt/sb-script/internal/store"
	storage "github.com/slidebolt/sb-storage-sdk"
	storageserver "github.com/slidebolt/sb-storage-server"
)

// ---------------------------------------------------------------------------
// Scenario context
// ---------------------------------------------------------------------------

type bddCtx struct {
	t      *testing.T
	store  storage.Storage
	msg    messenger.Messenger
	engine *engine.Engine

	lastErr  error
	lastHash string

	mu       sync.Mutex
	received chan commandEvent
	history  []commandEvent
	subs     []messenger.Subscription
}

type commandEvent struct {
	subject string
	action  string
	data    []byte
}

func newBDDCtx(t *testing.T) *bddCtx {
	t.Helper()
	msg, err := messenger.Mock()
	if err != nil {
		t.Fatalf("messenger mock: %v", err)
	}
	t.Cleanup(func() { msg.Close() })
	store, err := storageserver.Mock(msg)
	if err != nil {
		t.Fatalf("storage mock: %v", err)
	}
	return &bddCtx{
		t:        t,
		msg:      msg,
		store:    store,
		received: make(chan commandEvent, 32),
	}
}

func (c *bddCtx) startEngine() {
	eng, err := engine.New(c.msg, c.store)
	if err != nil {
		c.t.Fatalf("start engine: %v", err)
	}
	c.engine = eng
	// Single cleanup uses c.engine so restartEngine swaps safely.
	c.t.Cleanup(func() {
		c.mu.Lock()
		subs := append([]messenger.Subscription(nil), c.subs...)
		c.subs = nil
		c.mu.Unlock()
		for _, sub := range subs {
			sub.Unsubscribe()
		}
		if c.engine != nil {
			c.engine.Shutdown()
			c.engine = nil
		}
	})
}

// ---------------------------------------------------------------------------
// Step definitions
// ---------------------------------------------------------------------------

func (c *bddCtx) RegisterSteps(ctx *godog.ScenarioContext) {
	// Setup
	ctx.Step(`^the scripting engine is running$`, c.theEngineIsRunning)
	ctx.Step(`^a light entity "([^"]*)" named "([^"]*)" with power (on|off)$`, c.aLightEntity)
	ctx.Step(`^a light entity "([^"]*)" named "([^"]*)" with color mode "([^"]*)"$`, c.aLightEntityWithColorMode)
	ctx.Step(`^a lightstrip entity "([^"]*)" named "([^"]*)" with (\d+) segments$`, c.aLightstripEntity)
	ctx.Step(`^a switch entity "([^"]*)" named "([^"]*)" with power (on|off)$`, c.aSwitchEntity)
	ctx.Step(`^a binary sensor entity "([^"]*)" named "([^"]*)" class "([^"]*)" with state (on|off)$`, c.aBinarySensorEntity)
	ctx.Step(`^a lock entity "([^"]*)" named "([^"]*)" that is (locked|unlocked)$`, c.aLockEntity)
	ctx.Step(`^a cover entity "([^"]*)" named "([^"]*)" at position (\d+)$`, c.aCoverEntity)
	ctx.Step(`^a button entity "([^"]*)" named "([^"]*)"$`, c.aButtonEntity)
	ctx.Step(`^a text entity "([^"]*)" named "([^"]*)" with value "([^"]*)"$`, c.aTextEntity)

	// RegisterScript
	ctx.Step(`^a script definition "([^"]*)" is saved from file "([^"]*)"$`, c.saveDefinitionFromFile)

	// StartScript / StopScript
	ctx.Step(`^I start script "([^"]*)" with query "([^"]*)"$`, c.iStartScript)
	ctx.Step(`^I start script "([^"]*)" with query "([^"]*)" again$`, c.iStartScript)
	ctx.Step(`^I stop script "([^"]*)" with query "([^"]*)"$`, c.iStopScript)
	ctx.Step(`^no error occurred$`, c.noError)
	ctx.Step(`^an instance exists for "([^"]*)" with query "([^"]*)"$`, c.instanceExists)
	ctx.Step(`^no instance exists for "([^"]*)" with query "([^"]*)"$`, c.instanceNotExists)
	ctx.Step(`^an error is expected$`, c.anErrorIsExpected)

	// Timer / command assertions
	ctx.Step(`^within (\d+) milliseconds I receive command "([^"]*)" on "([^"]*)"$`, c.withinMsReceiveCommand)
	ctx.Step(`^within (\d+) milliseconds command "([^"]*)" reaches "([^"]*)"$`, c.withinMsCommandReachesEntity)
	ctx.Step(`^within (\d+) milliseconds command "([^"]*)" reaches "([^"]*)" with payload:$`, c.withinMsCommandReachesEntityWithPayload)
	ctx.Step(`^I subscribe to commands on "([^"]*)"$`, c.subscribeToCommands)
	ctx.Step(`^I clear observed commands$`, c.clearObservedCommands)
	ctx.Step(`^no command is received within (\d+) milliseconds$`, c.noCommandWithinMs)
	ctx.Step(`^no command reaches "([^"]*)" within (\d+) milliseconds$`, c.noCommandReachesEntityWithinMs)
	ctx.Step(`^within (\d+) milliseconds each entity receives at least (\d+) "([^"]*)" commands:$`, c.withinMsEachEntityReceivesAtLeast)
	ctx.Step(`^at least (\d+) commands arrive on "([^"]*)" within (\d+) milliseconds$`, c.atLeastNCommandsArriveWithinMs)

	// BindEntity
	ctx.Step(`^the entity "([^"]*)" state changes$`, c.entityStateChanges)
	ctx.Step(`^the binary sensor entity "([^"]*)" changes to (on|off)$`, c.binarySensorChangesTo)
	ctx.Step(`^the button entity "([^"]*)" is pressed$`, c.buttonPressed)
	ctx.Step(`^the text entity "([^"]*)" changes to "([^"]*)"$`, c.textEntityChangesTo)
	ctx.Step(`^I wait (\d+) milliseconds$`, c.iWaitMilliseconds)
	ctx.Step(`^within (\d+) milliseconds the signal "([^"]*)" is received$`, c.withinMsSignalReceived)

	// State recovery
	ctx.Step(`^the engine is restarted$`, c.restartEngine)
}

func (c *bddCtx) theEngineIsRunning() error {
	c.startEngine()
	return nil
}

func (c *bddCtx) aLightEntity(key, name, power string) error {
	plugin, device, id, err := parseKey(key)
	if err != nil {
		return err
	}
	e := domain.Entity{
		ID: id, Plugin: plugin, DeviceID: device,
		Type: "light", Name: name,
		State: domain.Light{Power: power == "on"},
	}
	return c.store.Save(e)
}

func (c *bddCtx) aSwitchEntity(key, name, power string) error {
	plugin, device, id, err := parseKey(key)
	if err != nil {
		return err
	}
	e := domain.Entity{
		ID: id, Plugin: plugin, DeviceID: device,
		Type: "switch", Name: name,
		State: domain.Switch{Power: power == "on"},
	}
	return c.store.Save(e)
}

func (c *bddCtx) aLightEntityWithColorMode(key, name, colorMode string) error {
	plugin, device, id, err := parseKey(key)
	if err != nil {
		return err
	}
	e := domain.Entity{
		ID: id, Plugin: plugin, DeviceID: device,
		Type: "light", Name: name,
		State: domain.Light{Power: false, ColorMode: colorMode},
	}
	return c.store.Save(e)
}

func (c *bddCtx) aLightstripEntity(key, name string, segments int) error {
	plugin, device, id, err := parseKey(key)
	if err != nil {
		return err
	}
	stripSegments := make([]domain.Segment, 0, segments)
	for i := 1; i <= segments; i++ {
		stripSegments = append(stripSegments, domain.Segment{
			ID:         i,
			RGB:        []int{0, 0, 0},
			Brightness: 0,
		})
	}
	e := domain.Entity{
		ID: id, Plugin: plugin, DeviceID: device,
		Type: "lightstrip", Name: name,
		State: domain.LightStrip{
			Power:      false,
			Brightness: 0,
			ColorMode:  "rgb",
			Segments:   stripSegments,
		},
	}
	return c.store.Save(e)
}

func (c *bddCtx) aBinarySensorEntity(key, name, class, state string) error {
	plugin, device, id, err := parseKey(key)
	if err != nil {
		return err
	}
	e := domain.Entity{
		ID: id, Plugin: plugin, DeviceID: device,
		Type: "binary_sensor", Name: name,
		State: domain.BinarySensor{On: state == "on", DeviceClass: class},
	}
	return c.store.Save(e)
}

func (c *bddCtx) aLockEntity(key, name, state string) error {
	plugin, device, id, err := parseKey(key)
	if err != nil {
		return err
	}
	e := domain.Entity{
		ID: id, Plugin: plugin, DeviceID: device,
		Type: "lock", Name: name,
		State: domain.Lock{Locked: state == "locked"},
	}
	return c.store.Save(e)
}

func (c *bddCtx) aCoverEntity(key, name string, position int) error {
	plugin, device, id, err := parseKey(key)
	if err != nil {
		return err
	}
	e := domain.Entity{
		ID: id, Plugin: plugin, DeviceID: device,
		Type: "cover", Name: name,
		State: domain.Cover{Position: position},
	}
	return c.store.Save(e)
}

func (c *bddCtx) aButtonEntity(key, name string) error {
	plugin, device, id, err := parseKey(key)
	if err != nil {
		return err
	}
	e := domain.Entity{
		ID: id, Plugin: plugin, DeviceID: device,
		Type: "button", Name: name,
		State: domain.Button{Presses: 0},
	}
	return c.store.Save(e)
}

func (c *bddCtx) aTextEntity(key, name, value string) error {
	plugin, device, id, err := parseKey(key)
	if err != nil {
		return err
	}
	e := domain.Entity{
		ID: id, Plugin: plugin, DeviceID: device,
		Type: "text", Name: name,
		State: domain.Text{Value: value},
	}
	return c.store.Save(e)
}

func (c *bddCtx) saveDefinitionFromFile(name, relPath string) error {
	path := filepath.Join("features", relPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read lua fixture %s: %w", relPath, err)
	}
	return c.engine.SaveDefinition(name, string(data))
}

func (c *bddCtx) iStartScript(name, query string) error {
	hash, err := c.engine.StartScript(name, query)
	c.lastHash = hash
	c.lastErr = err
	return nil
}

func (c *bddCtx) iStopScript(name, query string) error {
	c.lastErr = c.engine.StopScript(name, query)
	return nil
}

func (c *bddCtx) noError() error {
	return c.lastErr
}

func (c *bddCtx) instanceExists(name, query string) error {
	hash := scriptstore.HashInstance(name, query)
	if _, err := c.store.Get(scriptstore.InstanceKey{Hash: hash}); err != nil {
		return fmt.Errorf("expected instance %s/%s (%s) to exist: %w", name, query, hash, err)
	}
	return nil
}

func (c *bddCtx) instanceNotExists(name, query string) error {
	hash := scriptstore.HashInstance(name, query)
	if _, err := c.store.Get(scriptstore.InstanceKey{Hash: hash}); err == nil {
		return fmt.Errorf("expected instance %s/%s (%s) to be absent", name, query, hash)
	}
	return nil
}

func (c *bddCtx) subscribeToCommands(pattern string) error {
	sub, err := c.msg.Subscribe(pattern, func(m *messenger.Message) {
		ev := commandEvent{subject: m.Subject, action: actionFromSubject(m.Subject), data: append([]byte(nil), m.Data...)}
		c.mu.Lock()
		c.history = append(c.history, ev)
		c.mu.Unlock()
		select {
		case c.received <- ev:
		default:
		}
	})
	if err == nil {
		c.mu.Lock()
		c.subs = append(c.subs, sub)
		c.mu.Unlock()
	}
	return err
}

func (c *bddCtx) clearObservedCommands() error {
	c.mu.Lock()
	c.history = nil
	c.mu.Unlock()
	for {
		select {
		case <-c.received:
		default:
			return nil
		}
	}
}

func (c *bddCtx) withinMsReceiveCommand(ms int, action, pattern string) error {
	if err := c.subscribeToCommands(pattern); err != nil {
		return err
	}
	return c.withinMsSignalReceived(ms, action)
}

func (c *bddCtx) withinMsSignalReceived(ms int, signal string) error {
	_, err := c.waitForEvent(ms, func(ev commandEvent) bool {
		return ev.action == signal
	})
	if err != nil {
		return fmt.Errorf("timed out after %dms waiting for signal %q", ms, signal)
	}
	return nil
}

func (c *bddCtx) withinMsCommandReachesEntity(ms int, action, entityKey string) error {
	if err := c.subscribeToCommands(entityKey + ".command.>"); err != nil {
		return err
	}
	_, err := c.waitForEvent(ms, func(ev commandEvent) bool {
		return ev.action == action && subjectHasEntityPrefix(ev.subject, entityKey)
	})
	if err != nil {
		return fmt.Errorf("timed out after %dms waiting for %q on %s", ms, action, entityKey)
	}
	return nil
}

func (c *bddCtx) withinMsCommandReachesEntityWithPayload(ms int, action, entityKey string, body *godog.DocString) error {
	if err := c.subscribeToCommands(entityKey + ".command.>"); err != nil {
		return err
	}
	want, err := normalizedJSON(body.Content)
	if err != nil {
		return fmt.Errorf("parse expected payload: %w", err)
	}
	ev, err := c.waitForEvent(ms, func(ev commandEvent) bool {
		if ev.action != action || !subjectHasEntityPrefix(ev.subject, entityKey) {
			return false
		}
		got, err := normalizedJSON(string(ev.data))
		if err != nil {
			return false
		}
		return reflect.DeepEqual(got, want)
	})
	if err != nil {
		return fmt.Errorf("timed out after %dms waiting for %q on %s", ms, action, entityKey)
	}
	_ = ev
	return nil
}

func (c *bddCtx) entityStateChanges(key string) error {
	return c.updateEntity(key, func(ent *domain.Entity) error { return nil })
}

func (c *bddCtx) binarySensorChangesTo(key, state string) error {
	return c.updateEntity(key, func(ent *domain.Entity) error {
		bs, ok := ent.State.(domain.BinarySensor)
		if !ok {
			return fmt.Errorf("entity %s is not a binary sensor", key)
		}
		bs.On = state == "on"
		ent.State = bs
		return nil
	})
}

func (c *bddCtx) buttonPressed(key string) error {
	return c.updateEntity(key, func(ent *domain.Entity) error {
		btn, ok := ent.State.(domain.Button)
		if !ok {
			return fmt.Errorf("entity %s is not a button", key)
		}
		btn.Presses++
		ent.State = btn
		return nil
	})
}

func (c *bddCtx) textEntityChangesTo(key, value string) error {
	return c.updateEntity(key, func(ent *domain.Entity) error {
		txt, ok := ent.State.(domain.Text)
		if !ok {
			return fmt.Errorf("entity %s is not a text entity", key)
		}
		txt.Value = value
		ent.State = txt
		return nil
	})
}

func (c *bddCtx) iWaitMilliseconds(ms int) error {
	time.Sleep(time.Duration(ms) * time.Millisecond)
	return nil
}

// withinMsEachEntityReceivesAtLeast subscribes to each entity's commands and
// counts arrivals, asserting each hits the minimum within the timeout.
func (c *bddCtx) withinMsEachEntityReceivesAtLeast(ms, minCount int, action string, table *godog.Table) error {
	type counter struct {
		mu    sync.Mutex
		count int
	}

	keys := make([]string, 0, len(table.Rows))
	counters := make(map[string]*counter)
	for _, row := range table.Rows {
		key := row.Cells[0].Value
		keys = append(keys, key)
		counters[key] = &counter{}
	}

	subs := make([]messenger.Subscription, 0, len(keys))
	for _, key := range keys {
		k := key
		cnt := counters[k]
		sub, err := c.msg.Subscribe(k+".command."+action, func(m *messenger.Message) {
			cnt.mu.Lock()
			cnt.count++
			cnt.mu.Unlock()
		})
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", k, err)
		}
		subs = append(subs, sub)
	}
	defer func() {
		for _, sub := range subs {
			sub.Unsubscribe()
		}
	}()

	deadline := time.Now().Add(time.Duration(ms) * time.Millisecond)
	for time.Now().Before(deadline) {
		allMet := true
		for _, cnt := range counters {
			cnt.mu.Lock()
			n := cnt.count
			cnt.mu.Unlock()
			if n < minCount {
				allMet = false
				break
			}
		}
		if allMet {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Report which entities fell short.
	var failures []string
	for _, key := range keys {
		cnt := counters[key]
		cnt.mu.Lock()
		n := cnt.count
		cnt.mu.Unlock()
		if n < minCount {
			failures = append(failures, fmt.Sprintf("%s: got %d, want >=%d", key, n, minCount))
		}
	}
	return fmt.Errorf("command count not met after %dms: %s", ms, strings.Join(failures, "; "))
}

// atLeastNCommandsArriveWithinMs blocks until n commands arrive on entityKey or times out.
func (c *bddCtx) atLeastNCommandsArriveWithinMs(n int, entityKey string, ms int) error {
	arrived := make(chan struct{}, n+10)
	sub, err := c.msg.Subscribe(entityKey+".command.>", func(m *messenger.Message) {
		arrived <- struct{}{}
	})
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()

	deadline := time.After(time.Duration(ms) * time.Millisecond)
	count := 0
	for count < n {
		select {
		case <-arrived:
			count++
		case <-deadline:
			return fmt.Errorf("only %d/%d commands arrived on %s within %dms", count, n, entityKey, ms)
		}
	}
	return nil
}

func (c *bddCtx) anErrorIsExpected() error {
	if c.lastErr == nil {
		return fmt.Errorf("expected an error but got nil")
	}
	return nil
}

// noCommandReachesEntityWithinMs subscribes to that entity's commands and
// asserts nothing arrives within the timeout.
func (c *bddCtx) noCommandReachesEntityWithinMs(entityKey string, ms int) error {
	if ev, ok := c.findEvent(func(ev commandEvent) bool {
		return subjectHasEntityPrefix(ev.subject, entityKey)
	}); ok {
		return fmt.Errorf("expected no command on %s but already received %q", entityKey, ev.action)
	}
	return c.assertNoEventWithin(ms, func(ev commandEvent) bool {
		return subjectHasEntityPrefix(ev.subject, entityKey)
	}, fmt.Sprintf("expected no command on %s", entityKey))
}

func (c *bddCtx) noCommandWithinMs(ms int) error {
	return c.assertNoEventWithin(ms, func(ev commandEvent) bool { return true }, "expected no command")
}

func (c *bddCtx) restartEngine() error {
	if c.engine != nil {
		c.engine.Shutdown()
		c.engine = nil
	}
	eng, err := engine.New(c.msg, c.store)
	if err != nil {
		return fmt.Errorf("restart engine: %w", err)
	}
	c.engine = eng
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func parseKey(key string) (plugin, device, id string, err error) {
	parts := splitDots(key)
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("key %q must have 3 dot-segments", key)
	}
	return parts[0], parts[1], parts[2], nil
}

func parseEntityKey(key string) domain.EntityKey {
	parts := splitDots(key)
	if len(parts) != 3 {
		return domain.EntityKey{}
	}
	return domain.EntityKey{Plugin: parts[0], DeviceID: parts[1], ID: parts[2]}
}

func splitDots(s string) []string {
	var parts []string
	cur := ""
	for _, r := range s {
		if r == '.' {
			parts = append(parts, cur)
			cur = ""
		} else {
			cur += string(r)
		}
	}
	parts = append(parts, cur)
	return parts
}

func (c *bddCtx) updateEntity(key string, mutate func(*domain.Entity) error) error {
	raw, err := c.store.Get(parseEntityKey(key))
	if err != nil {
		return fmt.Errorf("get entity %s: %w", key, err)
	}
	var ent domain.Entity
	if err := json.Unmarshal(raw, &ent); err != nil {
		return err
	}
	if err := mutate(&ent); err != nil {
		return err
	}
	return c.store.Save(ent)
}

func actionFromSubject(subject string) string {
	for i := len(subject) - 1; i >= 0; i-- {
		if subject[i] == '.' {
			return subject[i+1:]
		}
	}
	return subject
}

func subjectHasEntityPrefix(subject, entityKey string) bool {
	return strings.HasPrefix(subject, entityKey+".command.")
}

func (c *bddCtx) findEvent(match func(commandEvent) bool) (commandEvent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ev := range c.history {
		if match(ev) {
			return ev, true
		}
	}
	return commandEvent{}, false
}

func (c *bddCtx) waitForEvent(ms int, match func(commandEvent) bool) (commandEvent, error) {
	if ev, ok := c.findEvent(match); ok {
		return ev, nil
	}
	timeout := time.After(time.Duration(ms) * time.Millisecond)
	for {
		select {
		case ev := <-c.received:
			if match(ev) {
				return ev, nil
			}
		case <-timeout:
			return commandEvent{}, fmt.Errorf("timeout")
		}
	}
}

func (c *bddCtx) assertNoEventWithin(ms int, match func(commandEvent) bool, msg string) error {
	timeout := time.After(time.Duration(ms) * time.Millisecond)
	for {
		select {
		case ev := <-c.received:
			if match(ev) {
				return fmt.Errorf("%s but received %q on %s", msg, ev.action, ev.subject)
			}
		case <-timeout:
			return nil
		}
	}
}

func normalizedJSON(s string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, err
	}
	return v, nil
}
