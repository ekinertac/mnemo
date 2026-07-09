// Package opencode replay reads merged JSONL bytes and upserts them into a
// local SQLite database. It is the inverse of ExportSession/ExportMachineRows:
// export produces JSONL, replay consumes it. Path remapping handles
// machine-independent identity resolution so sessions captured on one machine
// can be restored on another with a different home directory.
//
// Related: internal/opencode/export.go (JSONL producer),
// internal/identity (identity encoding/decoding),
// internal/restore (higher-level restore orchestration).
package opencode

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ekinertac/mnemo/internal/identity"
)

// RowEvent is a single row from a JSONL export line.
type RowEvent struct {
	Type      string         `json:"type"`
	Table     string         `json:"table"`
	ID        string         `json:"id"`
	Data      map[string]any `json:"data"`
	Timestamp int64          `json:"timestamp"`
}

// parseRowEvent unmarshals a single JSONL line into a RowEvent and validates it.
func parseRowEvent(line []byte) (RowEvent, error) {
	var ev RowEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return ev, err
	}
	if ev.Type != "row" || ev.Table == "" || ev.ID == "" {
		return ev, fmt.Errorf("invalid row event: type=%q table=%q id=%q", ev.Type, ev.Table, ev.ID)
	}
	return ev, nil
}

// upsertRow inserts or replaces a row in the given table using the data from ev.
func upsertRow(db *sql.DB, ev RowEvent) error {
	cols := make([]string, 0, len(ev.Data))
	vals := make([]any, 0, len(ev.Data))
	for k, v := range ev.Data {
		cols = append(cols, k)
		vals = append(vals, v)
	}
	placeholders := make([]string, len(cols))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	stmt := fmt.Sprintf(
		"INSERT OR REPLACE INTO %s (%s) VALUES (%s)",
		ev.Table, strings.Join(cols, ", "), strings.Join(placeholders, ", "),
	)
	_, err := db.Exec(stmt, vals...)
	return err
}

// remapPaths rewrites directory and worktree columns in data from the source
// machine's paths to the local machine's paths, using the identity to determine
// how the path relates to home.
func remapPaths(data map[string]any, id identity.Identity, localHome string) {
	for _, col := range []string{"directory", "worktree"} {
		v, ok := data[col].(string)
		if !ok || v == "" {
			continue
		}
		data[col] = remapPath(v, id, localHome)
	}
}

// remapPath replaces the source home prefix with the local home prefix based on
// the identity. For home: identities the tail is decoded and joined with the
// local home. For abs: identities the source path is returned unchanged.
func remapPath(sourcePath string, id identity.Identity, localHome string) string {
	s := string(id)
	tail, ok := strings.CutPrefix(s, "home:")
	if !ok {
		return sourcePath
	}
	if tail == "" {
		return localHome
	}
	rel := strings.ReplaceAll(tail, "-", "/")
	rel = strings.TrimLeft(rel, "/")
	return filepath.Join(localHome, rel)
}

// ReplaySession reads merged JSONL bytes for a single session and upserts all
// rows into db. Paths in session and project rows are remapped from the source
// machine to the local machine using identity and localHome.
func ReplaySession(db *sql.DB, data []byte, id identity.Identity, localHome string) error {
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		ev, err := parseRowEvent(line)
		if err != nil {
			return fmt.Errorf("parse: %w", err)
		}
		if ev.Table == "session" || ev.Table == "project" {
			remapPaths(ev.Data, id, localHome)
		}
		if err := upsertRow(db, ev); err != nil {
			return fmt.Errorf("upsert %s.%s: %w", ev.Table, ev.ID, err)
		}
	}
	return nil
}

// ReplayMachineRows reads merged JSONL bytes for machine-scoped rows and
// upserts them into db. No path remapping is performed.
func ReplayMachineRows(db *sql.DB, data []byte) error {
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		ev, err := parseRowEvent(line)
		if err != nil {
			return err
		}
		if err := upsertRow(db, ev); err != nil {
			return fmt.Errorf("upsert %s.%s: %w", ev.Table, ev.ID, err)
		}
	}
	return nil
}

// RecomputeEventSequence recomputes the event_sequence table from the event
// table, setting each aggregate_id to its highest seq value.
func RecomputeEventSequence(db *sql.DB) error {
	_, err := db.Exec(`
		INSERT OR REPLACE INTO event_sequence (aggregate_id, seq)
		SELECT aggregate_id, MAX(seq) FROM event GROUP BY aggregate_id
	`)
	return err
}
