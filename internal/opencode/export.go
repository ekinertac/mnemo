// Package opencode export provides JSONL serialization for session and machine
// rows. Used by the sync logic to produce deterministic, sort-stable output that
// can be consumed by a remote peer for replay/merge.
//
// Each DB row becomes one JSONL line with the structure:
//
//	{"type":"row","table":"session","id":"ses_...","data":{...},"timestamp":1783570281045}
//
// Lines are sorted by timestamp for deterministic common-prefix ordering,
// which lets a consumer stream-merge two snapshots without loading everything
// into memory.
//
// Related: internal/command/sync.go (consumer of these exports),
// internal/opencode/opencode.go (row types and query helpers).
package opencode

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	_ "github.com/mattn/go-sqlite3"
)

// lineFromRow builds a single JSONL line from a table name, row ID, data map,
// and timestamp (epoch ms). The returned bytes are valid JSON without a trailing newline — the caller is responsible for joining lines.
func lineFromRow(table, id string, data map[string]any, timestamp int64) ([]byte, error) {
	rec := map[string]any{
		"type":      "row",
		"table":     table,
		"id":        id,
		"data":      data,
		"timestamp": timestamp,
	}
	return json.Marshal(rec)
}

// lineTimestamp is used by extractTimestamp to parse the timestamp from a line.
type lineTimestamp struct {
	Timestamp int64 `json:"timestamp"`
}

// extractTimestamp returns the timestamp field from a JSONL line, or 0 if it
// cannot be parsed.
func extractTimestamp(line []byte) int64 {
	var lt lineTimestamp
	if err := json.Unmarshal(line, &lt); err != nil {
		return 0
	}
	return lt.Timestamp
}

// addPtr adds a key-value pair to m when ptr is non-nil.
func addPtr[T any](m map[string]any, key string, ptr *T) {
	if ptr != nil {
		m[key] = *ptr
	}
}

// --- ToMap helpers ---

func sessionToMap(s SessionRow) map[string]any {
	m := map[string]any{
		"id":           s.ID,
		"project_id":   s.ProjectID,
		"directory":    s.Directory,
		"version":      s.Version,
		"time_created": s.TimeCreated,
		"time_updated": s.TimeUpdated,
	}
	addPtr(m, "title", s.Title)
	addPtr(m, "slug", s.Slug)
	addPtr(m, "permission", s.Permission)
	addPtr(m, "time_compacting", s.TimeCompacting)
	addPtr(m, "time_archived", s.TimeArchived)
	addPtr(m, "workspace_id", s.WorkspaceID)
	addPtr(m, "path", s.Path)
	addPtr(m, "agent", s.Agent)
	addPtr(m, "model", s.Model)
	addPtr(m, "cost", s.Cost)
	addPtr(m, "tokens_input", s.TokensInput)
	addPtr(m, "tokens_output", s.TokensOutput)
	addPtr(m, "tokens_reasoning", s.TokensReasoning)
	addPtr(m, "tokens_cache_read", s.TokensCacheRead)
	addPtr(m, "tokens_cache_write", s.TokensCacheWrite)
	addPtr(m, "metadata", s.Metadata)
	addPtr(m, "summary_additions", s.SummaryAdditions)
	addPtr(m, "summary_deletions", s.SummaryDeletions)
	addPtr(m, "summary_files", s.SummaryFiles)
	addPtr(m, "summary_diffs", s.SummaryDiffs)
	addPtr(m, "revert", s.Revert)
	addPtr(m, "parent_id", s.ParentID)
	return m
}

func messageToMap(m MessageRow) map[string]any {
	return map[string]any{
		"id":           m.ID,
		"session_id":   m.SessionID,
		"time_created": m.TimeCreated,
		"time_updated": m.TimeUpdated,
		"data":         m.Data,
	}
}

func partToMap(p PartRow) map[string]any {
	return map[string]any{
		"id":           p.ID,
		"message_id":   p.MessageID,
		"session_id":   p.SessionID,
		"time_created": p.TimeCreated,
		"time_updated": p.TimeUpdated,
		"data":         p.Data,
	}
}

func todoToMap(t TodoRow) map[string]any {
	m := map[string]any{
		"session_id":   t.SessionID,
		"content":      t.Content,
		"position":     t.Position,
		"time_created": t.TimeCreated,
		"time_updated": t.TimeUpdated,
	}
	addPtr(m, "status", t.Status)
	addPtr(m, "priority", t.Priority)
	return m
}

func eventToMap(e EventRow) map[string]any {
	return map[string]any{
		"id":           e.ID,
		"aggregate_id": e.AggregateID,
		"seq":          e.Seq,
		"type":         e.Type,
		"data":         e.Data,
	}
}

func sessionMessageToMap(s SessionMessageRow) map[string]any {
	return map[string]any{
		"id":           s.ID,
		"session_id":   s.SessionID,
		"type":         s.Type,
		"time_created": s.TimeCreated,
		"time_updated": s.TimeUpdated,
		"data":         s.Data,
		"seq":          s.Seq,
	}
}

func sessionInputToMap(s SessionInputRow) map[string]any {
	m := map[string]any{
		"id":           s.ID,
		"session_id":   s.SessionID,
		"prompt":       s.Prompt,
		"delivery":     s.Delivery,
		"time_created": s.TimeCreated,
	}
	addPtr(m, "admitted_seq", s.AdmittedSeq)
	addPtr(m, "promoted_seq", s.PromotedSeq)
	return m
}

func sessionContextEpochToMap(s SessionContextEpochRow) map[string]any {
	return map[string]any{
		"session_id":   s.SessionID,
		"baseline":     s.Baseline,
		"snapshot":     s.Snapshot,
		"baseline_seq": s.BaselineSeq,
	}
}

func credentialToMap(c CredentialRow) map[string]any {
	m := map[string]any{
		"id":             c.ID,
		"integration_id": c.IntegrationID,
		"label":          c.Label,
		"value":          c.Value,
		"active":         c.Active,
		"time_created":   c.TimeCreated,
		"time_updated":   c.TimeUpdated,
	}
	addPtr(m, "connector_id", c.ConnectorID)
	addPtr(m, "method_id", c.MethodID)
	return m
}

func accountToMap(a AccountRow) map[string]any {
	m := map[string]any{
		"id":           a.ID,
		"email":        a.Email,
		"url":          a.URL,
		"access_token": a.AccessToken,
		"time_created": a.TimeCreated,
		"time_updated": a.TimeUpdated,
	}
	addPtr(m, "refresh_token", a.RefreshToken)
	addPtr(m, "token_expiry", a.TokenExpiry)
	return m
}

func accountStateToMap(a AccountStateRow) map[string]any {
	m := map[string]any{
		"id": a.ID,
	}
	addPtr(m, "active_account_id", a.ActiveAccountID)
	addPtr(m, "active_org_id", a.ActiveOrgID)
	return m
}

func controlAccountToMap(c ControlAccountRow) map[string]any {
	m := map[string]any{
		"email":        c.Email,
		"url":          c.URL,
		"access_token": c.AccessToken,
		"active":       c.Active,
		"time_created": c.TimeCreated,
		"time_updated": c.TimeUpdated,
	}
	addPtr(m, "refresh_token", c.RefreshToken)
	addPtr(m, "token_expiry", c.TokenExpiry)
	return m
}

func permissionToMap(p PermissionRow) map[string]any {
	return map[string]any{
		"id":           p.ID,
		"project_id":   p.ProjectID,
		"action":       p.Action,
		"resource":     p.Resource,
		"time_created": p.TimeCreated,
		"time_updated": p.TimeUpdated,
	}
}

func projectToMap(p ProjectRow) map[string]any {
	m := map[string]any{
		"id":           p.ID,
		"worktree":     p.Worktree,
		"time_created": p.TimeCreated,
		"time_updated": p.TimeUpdated,
	}
	addPtr(m, "vcs", p.VCS)
	addPtr(m, "name", p.Name)
	addPtr(m, "icon_url", p.IconURL)
	addPtr(m, "icon_color", p.IconColor)
	addPtr(m, "time_initialized", p.TimeInitialized)
	addPtr(m, "sandboxes", p.Sandboxes)
	addPtr(m, "commands", p.Commands)
	addPtr(m, "icon_url_override", p.IconURLOverride)
	return m
}

// querySession returns a single session row by ID, or nil when not found.
func querySession(snap *sql.DB, sessionID string) (*SessionRow, error) {
	row := snap.QueryRow(sessionCols+" WHERE id = ?", sessionID)
	var r SessionRow
	err := row.Scan(
		&r.ID, &r.ProjectID, &r.Directory, &r.Title, &r.Slug,
		&r.Version, &r.Permission, &r.TimeCreated, &r.TimeUpdated,
		&r.TimeCompacting, &r.TimeArchived, &r.WorkspaceID, &r.Path,
		&r.Agent, &r.Model, &r.Cost, &r.TokensInput, &r.TokensOutput,
		&r.TokensReasoning, &r.TokensCacheRead, &r.TokensCacheWrite,
		&r.Metadata, &r.SummaryAdditions, &r.SummaryDeletions,
		&r.SummaryFiles, &r.SummaryDiffs, &r.Revert, &r.ParentID,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query session: %w", err)
	}
	return &r, nil
}

// queryProjects returns all project rows.
func queryProjects(snap *sql.DB) ([]ProjectRow, error) {
	rows, err := snap.Query(projectCols)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	var result []ProjectRow
	for rows.Next() {
		var r ProjectRow
		if err := rows.Scan(
			&r.ID, &r.Worktree, &r.VCS, &r.Name, &r.IconURL, &r.IconColor,
			&r.TimeCreated, &r.TimeUpdated, &r.TimeInitialized,
			&r.Sandboxes, &r.Commands, &r.IconURLOverride,
		); err != nil {
			return nil, fmt.Errorf("scan project row: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

const projectCols = `SELECT id, worktree, vcs, name, icon_url, icon_color, time_created, time_updated, time_initialized, sandboxes, commands, icon_url_override FROM project`

// --- Export functions ---

// ExportSession serializes all rows belonging to the given session into JSONL
// bytes. Lines are sorted by timestamp for deterministic ordering.
func ExportSession(snap *sql.DB, sessionID string) ([]byte, error) {
	var lines [][]byte

	// Session row
	s, err := querySession(snap, sessionID)
	if err != nil {
		return nil, fmt.Errorf("export session: %w", err)
	}
	if s == nil {
		return nil, fmt.Errorf("session %q not found", sessionID)
	}
	line, err := lineFromRow("session", s.ID, sessionToMap(*s), s.TimeUpdated)
	if err != nil {
		return nil, fmt.Errorf("session row: %w", err)
	}
	lines = append(lines, line)

	// Messages
	msgs, err := queryMessages(snap, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	for _, m := range msgs {
		line, err := lineFromRow("message", m.ID, messageToMap(m), m.TimeUpdated)
		if err != nil {
			return nil, fmt.Errorf("message row: %w", err)
		}
		lines = append(lines, line)
	}

	// Parts
	parts, err := queryParts(snap, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query parts: %w", err)
	}
	for _, p := range parts {
		line, err := lineFromRow("part", p.ID, partToMap(p), p.TimeUpdated)
		if err != nil {
			return nil, fmt.Errorf("part row: %w", err)
		}
		lines = append(lines, line)
	}

	// Todos — composite PK "sessionID|position"
	todos, err := queryTodos(snap, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query todos: %w", err)
	}
	for _, t := range todos {
		pk := t.SessionID + "|" + strconv.Itoa(int(t.Position))
		line, err := lineFromRow("todo", pk, todoToMap(t), t.TimeUpdated)
		if err != nil {
			return nil, fmt.Errorf("todo row: %w", err)
		}
		lines = append(lines, line)
	}

	// Events — no timestamp column, use 0
	events, err := queryEvents(snap, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	for _, e := range events {
		line, err := lineFromRow("event", e.ID, eventToMap(e), 0)
		if err != nil {
			return nil, fmt.Errorf("event row: %w", err)
		}
		lines = append(lines, line)
	}

	// session_message
	smsgs, err := querySessionMessages(snap, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query session_messages: %w", err)
	}
	for _, sm := range smsgs {
		line, err := lineFromRow("session_message", sm.ID, sessionMessageToMap(sm), sm.TimeUpdated)
		if err != nil {
			return nil, fmt.Errorf("session_message row: %w", err)
		}
		lines = append(lines, line)
	}

	// session_input
	sinputs, err := querySessionInputs(snap, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query session_inputs: %w", err)
	}
	for _, si := range sinputs {
		line, err := lineFromRow("session_input", si.ID, sessionInputToMap(si), si.TimeCreated)
		if err != nil {
			return nil, fmt.Errorf("session_input row: %w", err)
		}
		lines = append(lines, line)
	}

	// session_context_epoch
	epochs, err := querySessionContextEpochs(snap, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query session_context_epochs: %w", err)
	}
	for _, ep := range epochs {
		line, err := lineFromRow("session_context_epoch", ep.SessionID, sessionContextEpochToMap(ep), 0)
		if err != nil {
			return nil, fmt.Errorf("session_context_epoch row: %w", err)
		}
		lines = append(lines, line)
	}

	// Sort by timestamp
	sort.SliceStable(lines, func(i, j int) bool {
		return extractTimestamp(lines[i]) < extractTimestamp(lines[j])
	})

	return append(bytes.Join(lines, []byte("\n")), '\n'), nil
}

// ExportMachineRows serializes all machine-scoped rows (credential, account,
// account_state, control_account, permission, project) into JSONL bytes. Lines
// are sorted by timestamp for deterministic ordering.
func ExportMachineRows(snap *sql.DB) ([]byte, error) {
	var lines [][]byte

	machineRows, err := queryMachineRows(snap)
	if err != nil {
		return nil, fmt.Errorf("query machine rows: %w", err)
	}

	for table, rows := range machineRows {
		for _, row := range rows {
			var line []byte
			var err error
			switch r := row.(type) {
			case CredentialRow:
				line, err = lineFromRow(table, r.ID, credentialToMap(r), r.TimeUpdated)
			case AccountRow:
				line, err = lineFromRow(table, r.ID, accountToMap(r), r.TimeUpdated)
			case AccountStateRow:
				id := strconv.FormatInt(r.ID, 10)
				line, err = lineFromRow(table, id, accountStateToMap(r), 0)
			case ControlAccountRow:
				pk := r.Email + "|" + r.URL
				line, err = lineFromRow(table, pk, controlAccountToMap(r), r.TimeUpdated)
			case PermissionRow:
				line, err = lineFromRow(table, r.ID, permissionToMap(r), r.TimeUpdated)
			default:
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("%s row: %w", table, err)
			}
			lines = append(lines, line)
		}
	}

	// Projects
	projects, err := queryProjects(snap)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	for _, p := range projects {
		line, err := lineFromRow("project", p.ID, projectToMap(p), p.TimeUpdated)
		if err != nil {
			return nil, fmt.Errorf("project row: %w", err)
		}
		lines = append(lines, line)
	}

	sort.SliceStable(lines, func(i, j int) bool {
		return extractTimestamp(lines[i]) < extractTimestamp(lines[j])
	})

	return append(bytes.Join(lines, []byte("\n")), '\n'), nil
}
