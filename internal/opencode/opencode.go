// Package opencode provides helpers for working with OpenCode's SQLite database:
// DB connection, snapshots, identity resolution, row types, and query helpers.
// The row types are consumed by the sync logic in internal/command.
//
// Related: internal/identity (identity encoding/decoding),
// internal/manifest (identity resolution across machines),
// internal/command/sync.go (session sync consumer).
package opencode

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"

	"github.com/ekinertac/mnemo/internal/identity"
	_ "github.com/mattn/go-sqlite3"
)

// ErrDBNotFound is returned when the OpenCode SQLite database does not exist.
var ErrDBNotFound = fmt.Errorf("opencode database not found")

// OpenDBReadOnly opens an OpenCode SQLite database in read-only mode. Returns
// ErrDBNotFound when the file does not exist.
func OpenDBReadOnly(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, ErrDBNotFound
	}
	db, err := sql.Open("sqlite3", path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open opencode db: %w", err)
	}
	return db, nil
}

// SnapshotDB creates a temporary copy of an OpenCode SQLite database using the
// sqlite3 CLI's .backup command. The caller must remove the returned temp file.
func SnapshotDB(path string) (snapshotPath string, err error) {
	f, err := os.CreateTemp("", "mnemo-oc-*.db")
	if err != nil {
		return "", fmt.Errorf("create temp snapshot: %w", err)
	}
	snap := f.Name()
	f.Close()
	cmd := exec.Command("sqlite3", path, ".backup "+snap)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(snap)
		return "", fmt.Errorf("sqlite3 backup: %w\n%s", err, out)
	}
	return snap, nil
}

// IdentifyFromDirectory computes a machine-independent identity for a project
// directory given the current machine's encoded-home prefix.
func IdentifyFromDirectory(dir, encHome string) identity.Identity {
	return identity.FromEncoded(identity.Encode(dir), encHome)
}

// Row types for the OpenCode SQLite schema. Each maps one-to-one with a table.
// Pointer fields mark SQLite nullable columns; Scan handles NULL into *T.

// SessionRow maps the session table.
type SessionRow struct {
	ID               string
	ProjectID        string
	Directory        string
	Title            *string
	Slug             *string
	Version          string
	Permission       *string
	TimeCreated      int64
	TimeUpdated      int64
	TimeCompacting   *int64
	TimeArchived     *int64
	WorkspaceID      *string
	Path             *string
	Agent            *string
	Model            *string
	Cost             *float64
	TokensInput      *int64
	TokensOutput     *int64
	TokensReasoning  *int64
	TokensCacheRead  *int64
	TokensCacheWrite *int64
	Metadata         *string
	SummaryAdditions *int64
	SummaryDeletions *int64
	SummaryFiles     *int64
	SummaryDiffs     *string
	Revert           *string
	ParentID         *string
}

// MessageRow maps the message table.
type MessageRow struct {
	ID          string
	SessionID   string
	TimeCreated int64
	TimeUpdated int64
	Data        string
}

// PartRow maps the part table.
type PartRow struct {
	ID          string
	MessageID   string
	SessionID   string
	TimeCreated int64
	TimeUpdated int64
	Data        string
}

// TodoRow maps the todo table.
type TodoRow struct {
	SessionID   string
	Content     string
	Status      *string
	Priority    *string
	Position    int64
	TimeCreated int64
	TimeUpdated int64
}

// EventRow maps the event table.
type EventRow struct {
	ID          string
	AggregateID string
	Seq         int64
	Type        string
	Data        string
}

// EventSequenceRow maps the event_sequence table.
type EventSequenceRow struct {
	AggregateID string
	Seq         int64
	OwnerID     string
}

// SessionMessageRow maps the session_message table.
type SessionMessageRow struct {
	ID          string
	SessionID   string
	Type        string
	TimeCreated int64
	TimeUpdated int64
	Data        string
	Seq         int64
}

// SessionInputRow maps the session_input table.
type SessionInputRow struct {
	ID          string
	SessionID   string
	Prompt      string
	Delivery    string
	AdmittedSeq *int64
	PromotedSeq *int64
	TimeCreated int64
}

// SessionContextEpochRow maps the session_context_epoch table.
type SessionContextEpochRow struct {
	SessionID   string
	Baseline    string
	Snapshot    string
	BaselineSeq int64
}

// ProjectRow maps the project table.
type ProjectRow struct {
	ID              string
	Worktree        string
	VCS             *string
	Name            *string
	IconURL         *string
	IconColor       *string
	TimeCreated     int64
	TimeUpdated     int64
	TimeInitialized *int64
	Sandboxes       *string
	Commands        *string
	IconURLOverride *string
}

// CredentialRow maps the credential table.
type CredentialRow struct {
	ID            string
	IntegrationID string
	Label         string
	Value         string
	ConnectorID   *string
	MethodID      *string
	Active        int64
	TimeCreated   int64
	TimeUpdated   int64
}

// AccountRow maps the account table.
type AccountRow struct {
	ID           string
	Email        string
	URL          string
	AccessToken  string
	RefreshToken *string
	TokenExpiry  *int64
	TimeCreated  int64
	TimeUpdated  int64
}

// AccountStateRow maps the account_state table.
type AccountStateRow struct {
	ID              int64
	ActiveAccountID *string
	ActiveOrgID     *string
}

// ControlAccountRow maps the control_account table.
type ControlAccountRow struct {
	Email        string
	URL          string
	AccessToken  string
	RefreshToken *string
	TokenExpiry  *int64
	Active       int64
	TimeCreated  int64
	TimeUpdated  int64
}

// PermissionRow maps the permission table.
type PermissionRow struct {
	ID          string
	ProjectID   string
	Action      string
	Resource    string
	TimeCreated int64
	TimeUpdated int64
}

// query helpers — each returns all rows for the relevant table, optionally
// filtered by session_id for child tables.

func querySessions(db *sql.DB) ([]SessionRow, error) {
	rows, err := db.Query(sessionCols)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()

	var result []SessionRow
	for rows.Next() {
		var r SessionRow
		if err := rows.Scan(
			&r.ID, &r.ProjectID, &r.Directory, &r.Title, &r.Slug,
			&r.Version, &r.Permission, &r.TimeCreated, &r.TimeUpdated,
			&r.TimeCompacting, &r.TimeArchived, &r.WorkspaceID, &r.Path,
			&r.Agent, &r.Model, &r.Cost, &r.TokensInput, &r.TokensOutput,
			&r.TokensReasoning, &r.TokensCacheRead, &r.TokensCacheWrite,
			&r.Metadata, &r.SummaryAdditions, &r.SummaryDeletions,
			&r.SummaryFiles, &r.SummaryDiffs, &r.Revert, &r.ParentID,
		); err != nil {
			return nil, fmt.Errorf("scan session row: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func queryMessages(db *sql.DB, sessionID string) ([]MessageRow, error) {
	rows, err := db.Query(messageCols, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var result []MessageRow
	for rows.Next() {
		var r MessageRow
		if err := rows.Scan(&r.ID, &r.SessionID, &r.TimeCreated, &r.TimeUpdated, &r.Data); err != nil {
			return nil, fmt.Errorf("scan message row: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func queryParts(db *sql.DB, sessionID string) ([]PartRow, error) {
	rows, err := db.Query(partCols, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query parts: %w", err)
	}
	defer rows.Close()

	var result []PartRow
	for rows.Next() {
		var r PartRow
		if err := rows.Scan(&r.ID, &r.MessageID, &r.SessionID, &r.TimeCreated, &r.TimeUpdated, &r.Data); err != nil {
			return nil, fmt.Errorf("scan part row: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func queryTodos(db *sql.DB, sessionID string) ([]TodoRow, error) {
	rows, err := db.Query(todoCols, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query todos: %w", err)
	}
	defer rows.Close()

	var result []TodoRow
	for rows.Next() {
		var r TodoRow
		if err := rows.Scan(&r.SessionID, &r.Content, &r.Status, &r.Priority, &r.Position, &r.TimeCreated, &r.TimeUpdated); err != nil {
			return nil, fmt.Errorf("scan todo row: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func queryEvents(db *sql.DB, sessionID string) ([]EventRow, error) {
	rows, err := db.Query(eventCols, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var result []EventRow
	for rows.Next() {
		var r EventRow
		if err := rows.Scan(&r.ID, &r.AggregateID, &r.Seq, &r.Type, &r.Data); err != nil {
			return nil, fmt.Errorf("scan event row: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func querySessionMessages(db *sql.DB, sessionID string) ([]SessionMessageRow, error) {
	rows, err := db.Query(sessionMessageCols, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query session_messages: %w", err)
	}
	defer rows.Close()

	var result []SessionMessageRow
	for rows.Next() {
		var r SessionMessageRow
		if err := rows.Scan(&r.ID, &r.SessionID, &r.Type, &r.TimeCreated, &r.TimeUpdated, &r.Data, &r.Seq); err != nil {
			return nil, fmt.Errorf("scan session_message row: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func querySessionInputs(db *sql.DB, sessionID string) ([]SessionInputRow, error) {
	rows, err := db.Query(sessionInputCols, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query session_inputs: %w", err)
	}
	defer rows.Close()

	var result []SessionInputRow
	for rows.Next() {
		var r SessionInputRow
		if err := rows.Scan(&r.ID, &r.SessionID, &r.Prompt, &r.Delivery, &r.AdmittedSeq, &r.PromotedSeq, &r.TimeCreated); err != nil {
			return nil, fmt.Errorf("scan session_input row: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func querySessionContextEpochs(db *sql.DB, sessionID string) ([]SessionContextEpochRow, error) {
	rows, err := db.Query(sessionContextEpochCols, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query session_context_epochs: %w", err)
	}
	defer rows.Close()

	var result []SessionContextEpochRow
	for rows.Next() {
		var r SessionContextEpochRow
		if err := rows.Scan(&r.SessionID, &r.Baseline, &r.Snapshot, &r.BaselineSeq); err != nil {
			return nil, fmt.Errorf("scan session_context_epoch row: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// queryMachineRows returns credential, account, account_state, control_account,
// and permission rows keyed by table name. Each value is a slice of the
// corresponding row type.
func queryMachineRows(db *sql.DB) (map[string][]any, error) {
	result := make(map[string][]any)

	creds, err := func() ([]CredentialRow, error) {
		rows, err := db.Query(credentialCols)
		if err != nil {
			return nil, fmt.Errorf("query credentials: %w", err)
		}
		defer rows.Close()
		var out []CredentialRow
		for rows.Next() {
			var r CredentialRow
			if err := rows.Scan(&r.ID, &r.IntegrationID, &r.Label, &r.Value, &r.ConnectorID, &r.MethodID, &r.Active, &r.TimeCreated, &r.TimeUpdated); err != nil {
				return nil, fmt.Errorf("scan credential row: %w", err)
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}()
	if err != nil {
		return nil, err
	}
	result["credential"] = toAnySlice(creds)

	accts, err := func() ([]AccountRow, error) {
		rows, err := db.Query(accountCols)
		if err != nil {
			return nil, fmt.Errorf("query accounts: %w", err)
		}
		defer rows.Close()
		var out []AccountRow
		for rows.Next() {
			var r AccountRow
			if err := rows.Scan(&r.ID, &r.Email, &r.URL, &r.AccessToken, &r.RefreshToken, &r.TokenExpiry, &r.TimeCreated, &r.TimeUpdated); err != nil {
				return nil, fmt.Errorf("scan account row: %w", err)
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}()
	if err != nil {
		return nil, err
	}
	result["account"] = toAnySlice(accts)

	states, err := func() ([]AccountStateRow, error) {
		rows, err := db.Query(accountStateCols)
		if err != nil {
			return nil, fmt.Errorf("query account_state: %w", err)
		}
		defer rows.Close()
		var out []AccountStateRow
		for rows.Next() {
			var r AccountStateRow
			if err := rows.Scan(&r.ID, &r.ActiveAccountID, &r.ActiveOrgID); err != nil {
				return nil, fmt.Errorf("scan account_state row: %w", err)
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}()
	if err != nil {
		return nil, err
	}
	result["account_state"] = toAnySlice(states)

	ctrls, err := func() ([]ControlAccountRow, error) {
		rows, err := db.Query(controlAccountCols)
		if err != nil {
			return nil, fmt.Errorf("query control_account: %w", err)
		}
		defer rows.Close()
		var out []ControlAccountRow
		for rows.Next() {
			var r ControlAccountRow
			if err := rows.Scan(&r.Email, &r.URL, &r.AccessToken, &r.RefreshToken, &r.TokenExpiry, &r.Active, &r.TimeCreated, &r.TimeUpdated); err != nil {
				return nil, fmt.Errorf("scan control_account row: %w", err)
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}()
	if err != nil {
		return nil, err
	}
	result["control_account"] = toAnySlice(ctrls)

	perms, err := func() ([]PermissionRow, error) {
		rows, err := db.Query(permissionCols)
		if err != nil {
			return nil, fmt.Errorf("query permissions: %w", err)
		}
		defer rows.Close()
		var out []PermissionRow
		for rows.Next() {
			var r PermissionRow
			if err := rows.Scan(&r.ID, &r.ProjectID, &r.Action, &r.Resource, &r.TimeCreated, &r.TimeUpdated); err != nil {
				return nil, fmt.Errorf("scan permission row: %w", err)
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}()
	if err != nil {
		return nil, err
	}
	result["permission"] = toAnySlice(perms)

	return result, nil
}

func toAnySlice[T any](s []T) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// Column lists — kept separate for readability and reuse in tests.
const sessionCols = `SELECT id, project_id, directory, title, slug, version, permission, time_created, time_updated, time_compacting, time_archived, workspace_id, path, agent, model, cost, tokens_input, tokens_output, tokens_reasoning, tokens_cache_read, tokens_cache_write, metadata, summary_additions, summary_deletions, summary_files, summary_diffs, revert, parent_id FROM session`

const messageCols = `SELECT id, session_id, time_created, time_updated, data FROM message WHERE session_id = ?`

const partCols = `SELECT id, message_id, session_id, time_created, time_updated, data FROM part WHERE session_id = ?`

const todoCols = `SELECT session_id, content, status, priority, position, time_created, time_updated FROM todo WHERE session_id = ?`

const eventCols = `SELECT id, aggregate_id, seq, type, data FROM event WHERE aggregate_id = ?`

const sessionMessageCols = `SELECT id, session_id, type, time_created, time_updated, data, seq FROM session_message WHERE session_id = ?`

const sessionInputCols = `SELECT id, session_id, prompt, delivery, admitted_seq, promoted_seq, time_created FROM session_input WHERE session_id = ?`

const sessionContextEpochCols = `SELECT session_id, baseline, snapshot, baseline_seq FROM session_context_epoch WHERE session_id = ?`

const credentialCols = `SELECT id, integration_id, label, value, connector_id, method_id, active, time_created, time_updated FROM credential`

const accountCols = `SELECT id, email, url, access_token, refresh_token, token_expiry, time_created, time_updated FROM account`

const accountStateCols = `SELECT id, active_account_id, active_org_id FROM account_state`

const controlAccountCols = `SELECT email, url, access_token, refresh_token, token_expiry, active, time_created, time_updated FROM control_account`

const permissionCols = `SELECT id, project_id, action, resource, time_created, time_updated FROM permission`
