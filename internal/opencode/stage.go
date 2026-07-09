// Stage writes OpenCode sessions to a staging tree, grouped by identity.
// It snapshots the live DB to avoid holding a lock during the potentially
// slow export+write loop, then writes per-session JSONL files under
// by-id/<identity>/opencode-sessions/ and machine-scoped rows to
// opencode-machine.jsonl at the stage root.
//
// The function is the entry point for the "mnemo stage" command. It consumes
// SnapshotDB, OpenDBReadOnly, IdentifyFromDirectory (from internal/opencode),
// ExportSession, ExportMachineRows (from internal/opencode/export.go), and
// identity.PathSafe (from internal/identity).
//
// Missing DB or zero sessions are skipped silently (returns 0, nil).
// Related: internal/command/sync.go (consumer), internal/identity (identity
// encoding/decoding), internal/restore (inverse of stage).
package opencode

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ekinertac/mnemo/internal/identity"
)

// Stage snapshots the OpenCode database at dbPath, exports every session as
// JSONL grouped by identity under stageRoot/by-id/<identity>/opencode-sessions/,
// and writes machine-scoped rows to stageRoot/opencode-machine.jsonl.
// Returns the number of sessions staged, or 0 if the DB does not exist or has
// no sessions.
func Stage(dbPath, stageRoot, encHome string) (int, error) {
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return 0, nil
	}

	snap, err := SnapshotDB(dbPath)
	if err != nil {
		return 0, fmt.Errorf("snapshot: %w", err)
	}
	defer os.Remove(snap)

	db, err := OpenDBReadOnly(snap)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	sessions, err := querySessions(db)
	if err != nil {
		return 0, fmt.Errorf("query sessions: %w", err)
	}
	if len(sessions) == 0 {
		return 0, nil
	}

	byIdentity := map[identity.Identity][]SessionRow{}
	for _, s := range sessions {
		dir := s.Directory
		if dir == "" || s.ProjectID == "global" {
			dir = "/"
		}
		id := IdentifyFromDirectory(dir, encHome)
		byIdentity[id] = append(byIdentity[id], s)
	}

	for id, ss := range byIdentity {
		idSafe := identity.PathSafe(id)
		baseDir := filepath.Join(stageRoot, "by-id", idSafe, "opencode-sessions")
		if err := os.MkdirAll(baseDir, 0755); err != nil {
			return 0, fmt.Errorf("create staging dir %s: %w", baseDir, err)
		}
		for _, s := range ss {
			data, err := ExportSession(db, s.ID)
			if err != nil {
				return 0, fmt.Errorf("export session %s: %w", s.ID, err)
			}
			dst := filepath.Join(baseDir, s.ID+".jsonl")
			if err := os.WriteFile(dst, data, 0644); err != nil {
				return 0, fmt.Errorf("write %s: %w", dst, err)
			}
		}
	}

	machineData, err := ExportMachineRows(db)
	if err != nil {
		return 0, fmt.Errorf("export machine rows: %w", err)
	}
	if len(machineData) > 0 {
		if err := os.WriteFile(filepath.Join(stageRoot, "opencode-machine.jsonl"), machineData, 0644); err != nil {
			return 0, fmt.Errorf("write machine jsonl: %w", err)
		}
	}

	return len(sessions), nil
}
