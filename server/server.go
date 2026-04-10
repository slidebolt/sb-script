// Package server provides the sb-script scripting engine as an importable
// service. Used by the sb-script binary (production) and sb-manager-sdk
// TestEnv (testing) — the same way sb-storage-server is used by both.
package server

import (
	"encoding/json"
	"fmt"

	logging "github.com/slidebolt/sb-logging-sdk"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	"github.com/slidebolt/sb-script/internal/engine"
	storage "github.com/slidebolt/sb-storage-sdk"
)

// Service is the sb-script scripting service. It owns the engine and
// NATS API subscriptions for script.start and script.stop.
type Service struct {
	engine *engine.Engine
	subs   []messenger.Subscription
}

// New creates a scripting service connected to the given messenger and storage.
// The engine is started immediately and NATS API subscriptions are registered.
func New(msg messenger.Messenger, store storage.Storage) (*Service, error) {
	return NewWithLogger(msg, store, nil)
}

func NewWithLogger(msg messenger.Messenger, store storage.Storage, logger logging.Store) (*Service, error) {
	eng, err := engine.NewWithLogger(msg, store, logger)
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
	Name     string `json:"name"`
	QueryRef string `json:"queryRef,omitempty"`
}

type stopRequest struct {
	Name     string `json:"name"`
	QueryRef string `json:"queryRef,omitempty"`
}

type stopAllRequest struct {
	QueryRef string `json:"queryRef,omitempty"`
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
	sub1, err := msg.Subscribe("script.start", func(m *messenger.Message) {
		var req startRequest
		if err := json.Unmarshal(m.Data, &req); err != nil {
			m.RespondWithHeaders(apiErr(err), messenger.CopyHeaders(m.Headers))
			return
		}
		hash, err := s.engine.StartScriptWithTrace(req.Name, req.QueryRef, messenger.TraceID(m.Headers))
		if err != nil {
			m.RespondWithHeaders(apiErr(err), messenger.CopyHeaders(m.Headers))
			return
		}
		m.RespondWithHeaders(okHash(hash), messenger.CopyHeaders(m.Headers))
	})
	if err != nil {
		return fmt.Errorf("subscribe script.start: %w", err)
	}
	s.subs = append(s.subs, sub1)

	sub2, err := msg.Subscribe("script.stop", func(m *messenger.Message) {
		var req stopRequest
		if err := json.Unmarshal(m.Data, &req); err != nil {
			m.RespondWithHeaders(apiErr(err), messenger.CopyHeaders(m.Headers))
			return
		}
		if err := s.engine.StopScriptWithTrace(req.Name, req.QueryRef, messenger.TraceID(m.Headers)); err != nil {
			m.RespondWithHeaders(apiErr(err), messenger.CopyHeaders(m.Headers))
			return
		}
		m.RespondWithHeaders(ok(), messenger.CopyHeaders(m.Headers))
	})
	if err != nil {
		return fmt.Errorf("subscribe script.stop: %w", err)
	}
	s.subs = append(s.subs, sub2)

	sub3, err := msg.Subscribe("script.stop_all", func(m *messenger.Message) {
		var req stopAllRequest
		if err := json.Unmarshal(m.Data, &req); err != nil {
			m.RespondWithHeaders(apiErr(err), messenger.CopyHeaders(m.Headers))
			return
		}
		if err := s.engine.StopAllScriptsWithTrace(req.QueryRef, messenger.TraceID(m.Headers)); err != nil {
			m.RespondWithHeaders(apiErr(err), messenger.CopyHeaders(m.Headers))
			return
		}
		m.RespondWithHeaders(ok(), messenger.CopyHeaders(m.Headers))
	})
	if err != nil {
		return fmt.Errorf("subscribe script.stop_all: %w", err)
	}
	s.subs = append(s.subs, sub3)

	return nil
}
