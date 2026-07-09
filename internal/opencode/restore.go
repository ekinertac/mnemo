// Package opencode restore walks a staging tree written by Stage and replays
// every session into a local OpenCode SQLite database. It is the consumer side
// of the sync pipeline: Stage produces JSONL files under by-id/<identity>/,
// Restore reads them back and upserts into the local DB.
//
// The walker iterates over by-id/<identity>/opencode-sessions/*.jsonl, resolves
// each identity via FromPathSafe, and calls ReplaySession for each. It then
// replays machine-scoped rows from opencode-machine.jsonl at the staging root.
// Finally it recomputes the event_sequence table.
//
// Related: internal/opencode/stage.go (producer of the staging tree),
// internal/opencode/replay.go (row-level replay with path remapping),
// internal/identity (identity encoding/decoding),
// internal/manifest (identity+override resolution).
package opencode

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ekinertac/mnemo/internal/identity"
	"github.com/ekinertac/mnemo/internal/manifest"
	_ "github.com/mattn/go-sqlite3"
)

// Restore walks the staging tree at restoredRoot and replays all sessions and
// machine rows into the local OpenCode database at dbPath. Missing DB or empty
// staging tree is non-fatal (warns to stderr, returns nil). Path remapping uses
// the local user's home directory (os.UserHomeDir) so home-relative paths from
// the source machine are rewritten to match this machine.
func Restore(restoredRoot, dbPath, host, encHome string, man *manifest.Manifest) error {
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "mnemo: warning: opencode db not found at %s — skipping restore\n", dbPath)
		return nil
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("open local opencode db: %w", err)
	}
	defer db.Close()

	localHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}

	byIDRoot := filepath.Join(restoredRoot, "by-id")
	idDirs, err := os.ReadDir(byIDRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	count := 0
	for _, idDir := range idDirs {
		id := identity.FromPathSafe(idDir.Name())
		sessionsDir := filepath.Join(byIDRoot, idDir.Name(), "opencode-sessions")
		files, err := os.ReadDir(sessionsDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(sessionsDir, f.Name()))
			if err != nil {
				return fmt.Errorf("read %s: %w", f.Name(), err)
			}
			if err := ReplaySession(db, data, id, localHome); err != nil {
				return fmt.Errorf("replay %s: %w", f.Name(), err)
			}
			count++
		}
	}

	machineFile := filepath.Join(restoredRoot, "opencode-machine.jsonl")
	if data, err := os.ReadFile(machineFile); err == nil {
		if err := ReplayMachineRows(db, data); err != nil {
			return fmt.Errorf("replay machine rows: %w", err)
		}
	}

	if err := RecomputeEventSequence(db); err != nil {
		return fmt.Errorf("recompute event_sequence: %w", err)
	}

	if count > 0 {
		fmt.Printf("mnemo: restored %d opencode sessions\n", count)
	}
	return nil
}
