package manifest

import (
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsEmpty(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != 1 || len(m.Machines) != 0 || len(m.Overrides) != 0 {
		t.Errorf("missing file should yield empty v1 manifest, got %+v", m)
	}
}

func TestOverrideAndMachineRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.json")
	m, _ := Load(path)
	m.SetOverride("darwin-mbp", "home:-Code-foo", "/Users/ekinertac/work/foo")
	m.TouchMachine("darwin-mbp", "2026-06-13T21:00:00Z")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}

	m2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := m2.Override("darwin-mbp", "home:-Code-foo")
	if !ok || got != "/Users/ekinertac/work/foo" {
		t.Errorf("override = %q,%v want /Users/ekinertac/work/foo,true", got, ok)
	}
	if m2.Machines["darwin-mbp"].LastSeen != "2026-06-13T21:00:00Z" {
		t.Errorf("lastSeen not persisted: %+v", m2.Machines["darwin-mbp"])
	}
	if _, ok := m2.Override("win-desktop", "home:-Code-foo"); ok {
		t.Error("override must be scoped per host")
	}
}
