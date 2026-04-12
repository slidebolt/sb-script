// Package store defines storage key types for sb-script.
package store

import (
	"fmt"
	"time"
)

// RootKey stores sb-script service metadata at the plugin root.
// Key form: sb-script
type RootKey struct{}

func (RootKey) Key() string { return "sb-script" }

// ScriptsKey stores global script collection metadata.
// Key form: sb-script.scripts
type ScriptsKey struct{}

func (ScriptsKey) Key() string { return "sb-script.scripts" }

type Root struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
}

func (Root) Key() string { return RootKey{}.Key() }

type Scripts struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
}

func (Scripts) Key() string { return ScriptsKey{}.Key() }

// DefinitionKey is the storage key for a script definition.
// Stored in the State target so it is API-visible and queryable.
// Key form: sb-script.scripts.{name}
type DefinitionKey struct {
	Name string
}

func (k DefinitionKey) Key() string {
	return fmt.Sprintf("sb-script.scripts.%s", k.Name)
}

// Definition is the stored form of a script definition.
type Definition struct {
	Type     string `json:"type"`
	Language string `json:"language,omitempty"`
	Name     string `json:"name"`
	Source   string `json:"source"`
}

func (d Definition) Key() string {
	return DefinitionKey{Name: d.Name}.Key()
}

// InstanceKey is the storage key for a script instance.
// Stored in the Internal target — private, not indexed.
// Key form: sb-script.instances.{hash}
type InstanceKey struct {
	Hash string
}

func (k InstanceKey) Key() string {
	return fmt.Sprintf("sb-script.instances.%s", k.Hash)
}

// Instance is the persisted record for a running script instance.
type Instance struct {
	Name            string         `json:"name"`
	QueryRef        string         `json:"queryRef,omitempty"`
	Hash            string         `json:"hash"`
	Status          string         `json:"status,omitempty"`
	Trigger         TriggerInfo    `json:"trigger,omitempty"`
	Targets         TargetInfo     `json:"targets,omitempty"`
	ResolvedTargets []string       `json:"resolvedTargets,omitempty"`
	StartedAt       *time.Time     `json:"startedAt,omitempty"`
	LastFiredAt     *time.Time     `json:"lastFiredAt,omitempty"`
	NextFireAt      *time.Time     `json:"nextFireAt,omitempty"`
	LastError       string         `json:"lastError,omitempty"`
	FireCount       int            `json:"fireCount,omitempty"`
	State           map[string]any `json:"state,omitempty"`
}

func (i Instance) Key() string {
	return InstanceKey{Hash: i.hash()}.Key()
}

func (i Instance) hash() string {
	return hashKey(i.Name, i.QueryRef)
}

// HashInstance returns the deterministic instance hash for a name+queryRef pair.
func HashInstance(name, queryRef string) string {
	return hashKey(name, queryRef)
}

// hashKey produces a short stable identifier from name+query.
// Uses FNV-1a so it is fast, deterministic, and dependency-free.
func hashKey(name, query string) string {
	const offset = uint64(14695981039346656037)
	const prime = uint64(1099511628211)
	h := offset
	for _, b := range []byte(name + "\x00" + query) {
		h ^= uint64(b)
		h *= prime
	}
	return fmt.Sprintf("%016x", h)
}

type TriggerInfo struct {
	Kind       string  `json:"kind,omitempty"`
	QueryRef   string  `json:"queryRef,omitempty"`
	Query      string  `json:"query,omitempty"`
	MinSeconds float64 `json:"minSeconds,omitempty"`
	MaxSeconds float64 `json:"maxSeconds,omitempty"`
	Expr       string  `json:"expr,omitempty"`
}

type TargetInfo struct {
	Kind     string `json:"kind,omitempty"`
	QueryRef string `json:"queryRef,omitempty"`
	Query    string `json:"query,omitempty"`
}
