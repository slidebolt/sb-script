// Package server provides the sb-script scripting engine as an importable
// service. Used by the sb-script binary (production) and sb-manager-sdk
// TestEnv (testing) — the same way sb-storage-server is used by both.
package server

import (
	"encoding/json"
	"fmt"

	messenger "github.com/slidebolt/sb-messenger-sdk"
	storage "github.com/slidebolt/sb-storage-sdk"
	"github.com/slidebolt/sb-script/internal/engine"
)

// Service is the sb-script scripting service. It owns the engine and
// NATS API subscriptions for script.save_definition, script.start, script.stop.
type Service struct {
	engine *engine.Engine
	subs   []messenger.Subscription
}

// New creates a scripting service connected to the given messenger and storage.
// The engine is started immediately and NATS API subscriptions are registered.
func New(msg messenger.Messenger, store storage.Storage) (*Service, error) {
	eng, err := engine.New(msg, store)
	if err != nil {
		return nil, fmt.Errorf("sb-script/server: start engine: %w", err)
	}
	svc := &Service{engine: eng}
	if err := svc.subscribeAPI(msg); err != nil {
		eng.Shutdown()
		return nil, fmt.Errorf("sb-script/server: subscribe API: %w", err)
	}
	return svc, nil
}

// Shutdown stops all scripts, timers, and NATS subscriptions.
func (s *Service) Shutdown() {
	for _, sub := range s.subs {
		sub.Unsubscribe()
	}
	s.engine.Shutdown()
}

// --- NATS API request/response types ---

type saveDefinitionRequest struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

type startRequest struct {
	Name  string `json:"name"`
	Query string `json:"query"`
}

type stopRequest struct {
	Name  string `json:"name"`
	Query string `json:"query"`
}

// APIResponse is the standard response envelope for all script.* API calls.
type APIResponse struct {
	OK    bool   `json:"ok"`
	Hash  string `json:"hash,omitempty"`
	Error string `json:"error,omitempty"`
}

func ok() []byte {
	b, _ := json.Marshal(APIResponse{OK: true})
	return b
}

func okHash(hash string) []byte {
	b, _ := json.Marshal(APIResponse{OK: true, Hash: hash})
	return b
}

func apiErr(err error) []byte {
	b, _ := json.Marshal(APIResponse{OK: false, Error: err.Error()})
	return b
}

func (s *Service) subscribeAPI(msg messenger.Messenger) error {
	sub1, err := msg.Subscribe("script.save_definition", func(m *messenger.Message) {
		var req saveDefinitionRequest
		if err := json.Unmarshal(m.Data, &req); err != nil {
			m.Respond(apiErr(err))
			return
		}
		if err := s.engine.SaveDefinition(req.Name, req.Source); err != nil {
			m.Respond(apiErr(err))
			return
		}
		m.Respond(ok())
	})
	if err != nil {
		return fmt.Errorf("subscribe script.save_definition: %w", err)
	}
	s.subs = append(s.subs, sub1)

	sub2, err := msg.Subscribe("script.start", func(m *messenger.Message) {
		var req startRequest
		if err := json.Unmarshal(m.Data, &req); err != nil {
			m.Respond(apiErr(err))
			return
		}
		hash, err := s.engine.StartScript(req.Name, req.Query)
		if err != nil {
			m.Respond(apiErr(err))
			return
		}
		m.Respond(okHash(hash))
	})
	if err != nil {
		return fmt.Errorf("subscribe script.start: %w", err)
	}
	s.subs = append(s.subs, sub2)

	sub3, err := msg.Subscribe("script.stop", func(m *messenger.Message) {
		var req stopRequest
		if err := json.Unmarshal(m.Data, &req); err != nil {
			m.Respond(apiErr(err))
			return
		}
		if err := s.engine.StopScript(req.Name, req.Query); err != nil {
			m.Respond(apiErr(err))
			return
		}
		m.Respond(ok())
	})
	if err != nil {
		return fmt.Errorf("subscribe script.stop: %w", err)
	}
	s.subs = append(s.subs, sub3)

	return nil
}
