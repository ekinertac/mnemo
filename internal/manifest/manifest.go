// Package manifest reads and writes projects.json, Mnemo's small per-repo map. Since M2 made
// identity<->local-path a reversible function (DESIGN §4.4), this file is no longer the crux of
// resolution; it carries only (a) per-host overrides set by `mnemo map` for projects that live
// at a non-default path on a machine, and (b) lightweight machine bookkeeping for the
// `machines`/`projects` views. It lives in the staging-tree root so restic versions it like any
// other file.
//
// Timestamps are passed in by callers (never time.Now() here) so the logic stays testable.
// Related: internal/identity, internal/command/{push,pull,map,projects,machines}.go.
package manifest

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
)

type Machine struct {
	LastSeen string `json:"lastSeen"`
}

type Manifest struct {
	Version   int                          `json:"version"`
	Machines  map[string]Machine           `json:"machines"`
	Overrides map[string]map[string]string `json:"overrides"` // host -> identity -> localPath
}

// Load reads a manifest; a missing file yields an empty v1 manifest (additive, never an error).
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.Version == 0 {
		m.Version = 1
	}
	if m.Machines == nil {
		m.Machines = map[string]Machine{}
	}
	if m.Overrides == nil {
		m.Overrides = map[string]map[string]string{}
	}
	return &m, nil
}

func New() *Manifest {
	return &Manifest{Version: 1, Machines: map[string]Machine{}, Overrides: map[string]map[string]string{}}
}

// Save writes the manifest as indented JSON (human-diffable in the repo).
func (m *Manifest) Save(path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func (m *Manifest) SetOverride(host, id, localPath string) {
	if m.Overrides[host] == nil {
		m.Overrides[host] = map[string]string{}
	}
	m.Overrides[host][id] = localPath
}

func (m *Manifest) Override(host, id string) (string, bool) {
	p, ok := m.Overrides[host][id]
	return p, ok
}

func (m *Manifest) TouchMachine(host, ts string) {
	m.Machines[host] = Machine{LastSeen: ts}
}
