// Package store defines storage key types for sb-script.
package store

import "fmt"

// DefinitionKey is the storage key for a script definition.
// Stored in the State target so it is API-visible and queryable.
// Key form: sb-script.definitions.{name}
type DefinitionKey struct {
	Name string
}

func (k DefinitionKey) Key() string {
	return fmt.Sprintf("sb-script.definitions.%s", k.Name)
}

// Definition is the stored form of a script definition.
type Definition struct {
	Name   string `json:"name"`
	Source string `json:"source"`
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
	Name  string          `json:"name"`
	Query string          `json:"query"`
	State map[string]any  `json:"state,omitempty"`
}

func (i Instance) Key() string {
	return InstanceKey{Hash: i.hash()}.Key()
}

func (i Instance) hash() string {
	return hashKey(i.Name, i.Query)
}

// HashInstance returns the deterministic instance hash for a name+query pair.
func HashInstance(name, query string) string {
	return hashKey(name, query)
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
