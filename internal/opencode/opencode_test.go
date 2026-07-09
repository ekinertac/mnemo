package opencode

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// setupTestDB creates a temporary SQLite database with the opencode schema and
// returns the open DB handle and the file path. The caller must close the DB
// and remove the file.
func setupTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()

	f, err := os.CreateTemp("", "mnemo-test-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	path := f.Name()
	f.Close()

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		os.Remove(path)
		t.Fatalf("open temp db: %v", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE session (
			id TEXT PRIMARY KEY, project_id TEXT, directory TEXT, title TEXT,
			slug TEXT, version TEXT, permission TEXT,
			time_created INTEGER, time_updated INTEGER,
			time_compacting INTEGER, time_archived INTEGER,
			workspace_id TEXT, path TEXT, agent TEXT, model TEXT,
			cost REAL,
			tokens_input INTEGER, tokens_output INTEGER,
			tokens_reasoning INTEGER, tokens_cache_read INTEGER,
			tokens_cache_write INTEGER,
			metadata TEXT,
			summary_additions INTEGER, summary_deletions INTEGER,
			summary_files INTEGER, summary_diffs TEXT,
			revert TEXT, parent_id TEXT
		);
		CREATE TABLE message (
			id TEXT PRIMARY KEY, session_id TEXT,
			time_created INTEGER, time_updated INTEGER, data TEXT
		);
		CREATE TABLE part (
			id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT,
			time_created INTEGER, time_updated INTEGER, data TEXT
		);
		CREATE TABLE todo (
			session_id TEXT, content TEXT, status TEXT, priority TEXT,
			position INTEGER,
			time_created INTEGER, time_updated INTEGER,
			PRIMARY KEY (session_id, position)
		);
		CREATE TABLE event (
			id TEXT PRIMARY KEY, aggregate_id TEXT,
			seq INTEGER, type TEXT, data TEXT
		);
		CREATE TABLE event_sequence (
			aggregate_id TEXT PRIMARY KEY,
			seq INTEGER, owner_id TEXT
		);
		CREATE TABLE session_message (
			id TEXT PRIMARY KEY, session_id TEXT, type TEXT,
			time_created INTEGER, time_updated INTEGER, data TEXT,
			seq INTEGER
		);
		CREATE TABLE session_input (
			id TEXT PRIMARY KEY, session_id TEXT,
			prompt TEXT, delivery TEXT,
			admitted_seq INTEGER, promoted_seq INTEGER,
			time_created INTEGER
		);
		CREATE TABLE session_context_epoch (
			session_id TEXT PRIMARY KEY,
			baseline TEXT, snapshot TEXT,
			baseline_seq INTEGER
		);
		CREATE TABLE project (
			id TEXT PRIMARY KEY, worktree TEXT, vcs TEXT, name TEXT,
			icon_url TEXT, icon_color TEXT,
			time_created INTEGER, time_updated INTEGER,
			time_initialized INTEGER,
			sandboxes TEXT, commands TEXT, icon_url_override TEXT
		);
		CREATE TABLE credential (
			id TEXT PRIMARY KEY, integration_id TEXT, label TEXT,
			value TEXT, connector_id TEXT, method_id TEXT,
			active INTEGER,
			time_created INTEGER, time_updated INTEGER
		);
		CREATE TABLE account (
			id TEXT PRIMARY KEY, email TEXT, url TEXT,
			access_token TEXT,
			refresh_token TEXT, token_expiry INTEGER,
			time_created INTEGER, time_updated INTEGER
		);
		CREATE TABLE account_state (
			id INTEGER PRIMARY KEY,
			active_account_id TEXT, active_org_id TEXT
		);
		CREATE TABLE control_account (
			email TEXT, url TEXT, access_token TEXT,
			refresh_token TEXT, token_expiry INTEGER,
			active INTEGER,
			time_created INTEGER, time_updated INTEGER,
			PRIMARY KEY (email, url)
		);
		CREATE TABLE permission (
			id TEXT PRIMARY KEY, project_id TEXT, action TEXT,
			resource TEXT,
			time_created INTEGER, time_updated INTEGER
		);
	`); err != nil {
		db.Close()
		os.Remove(path)
		t.Fatalf("create tables: %v", err)
	}

	return db, path
}

func TestOpenDBReadOnly_missing(t *testing.T) {
	_, err := OpenDBReadOnly(filepath.Join(t.TempDir(), "nonexistent.db"))
	if err != ErrDBNotFound {
		t.Fatalf("OpenDBReadOnly(nonexistent) = %v, want %v", err, ErrDBNotFound)
	}
}

func TestOpenDBReadOnly_valid(t *testing.T) {
	db, path := setupTestDB(t)
	db.Close()
	defer os.Remove(path)

	got, err := OpenDBReadOnly(path)
	if err != nil {
		t.Fatalf("OpenDBReadOnly(valid) = %v, want nil", err)
	}
	got.Close()
}

func TestOpenDBReadOnly_readOnly(t *testing.T) {
	db, path := setupTestDB(t)
	db.Close()
	defer os.Remove(path)

	ro, err := OpenDBReadOnly(path)
	if err != nil {
		t.Fatalf("OpenDBReadOnly: %v", err)
	}
	ro.Close()
}

func TestSnapshotDB(t *testing.T) {
	db, path := setupTestDB(t)
	db.Close()
	defer os.Remove(path)

	// Insert a session so the snapshot has content
	rw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = rw.Exec("INSERT INTO session (id, project_id, directory, version, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)",
		"snap-test-id", "proj", "/tmp", "1", 100, 100)
	rw.Close()
	if err != nil {
		t.Fatal(err)
	}

	snap, err := SnapshotDB(path)
	if err != nil {
		t.Fatalf("SnapshotDB: %v", err)
	}
	defer os.Remove(snap)

	if _, err := os.Stat(snap); os.IsNotExist(err) {
		t.Fatalf("snapshot file %s does not exist", snap)
	}

	// Verify snapshot is readable
	snapDB, err := sql.Open("sqlite3", snap+"?mode=ro")
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snapDB.Close()

	var count int
	if err := snapDB.QueryRow("SELECT COUNT(*) FROM session WHERE id = ?", "snap-test-id").Scan(&count); err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	if count != 1 {
		t.Fatalf("snapshot: expected 1 session, got %d", count)
	}
}

func TestIdentifyFromDirectory(t *testing.T) {
	id := IdentifyFromDirectory("/Users/ekinertac/Code/foo", "-Users-ekinertac")
	if id != "home:-Code-foo" {
		t.Fatalf("IdentifyFromDirectory = %q, want home:-Code-foo", id)
	}

	idAbs := IdentifyFromDirectory("/opt/bar", "-Users-ekinertac")
	if idAbs != "abs:-opt-bar" {
		t.Fatalf("IdentifyFromDirectory(abs) = %q, want abs:-opt-bar", idAbs)
	}
}

func TestQuerySessions(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	_, err := db.Exec(`INSERT INTO session (id, project_id, directory, version, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		"s1", "p1", "/dir1", "1.0", 100, 200)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO session (id, project_id, directory, version, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		"s2", "p2", "/dir2", "2.0", 300, 400)
	if err != nil {
		t.Fatal(err)
	}

	sessions, err := querySessions(db)
	if err != nil {
		t.Fatalf("querySessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	if sessions[0].ID != "s1" || sessions[0].ProjectID != "p1" || sessions[0].Directory != "/dir1" {
		t.Fatalf("unexpected session[0]: %+v", sessions[0])
	}
	if sessions[1].ID != "s2" || sessions[1].Version != "2.0" || sessions[1].TimeCreated != 300 {
		t.Fatalf("unexpected session[1]: %+v", sessions[1])
	}
}

func TestQueryMessages(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	_, err := db.Exec(`INSERT INTO session (id, project_id, directory, version, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		"sid", "p", "/d", "1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		"m1", "sid", 10, 20, "hello")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		"m2", "sid", 30, 40, "world")
	if err != nil {
		t.Fatal(err)
	}
	// Message for a different session — should not appear
	_, err = db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		"m3", "other", 50, 60, "ghost")
	if err != nil {
		t.Fatal(err)
	}

	msgs, err := queryMessages(db, "sid")
	if err != nil {
		t.Fatalf("queryMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].ID != "m1" || msgs[0].Data != "hello" {
		t.Fatalf("unexpected msg[0]: %+v", msgs[0])
	}
	if msgs[1].ID != "m2" || msgs[1].Data != "world" {
		t.Fatalf("unexpected msg[1]: %+v", msgs[1])
	}

	// No messages for unknown session
	empty, err := queryMessages(db, "nonexistent")
	if err != nil {
		t.Fatalf("queryMessages: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected 0 messages for unknown session, got %d", len(empty))
	}
}

func TestQueryParts(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	_, err := db.Exec(`INSERT INTO session (id, project_id, directory, version, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		"sid", "p", "/d", "1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?)`,
		"mid", "sid", 0, 0, "msg")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?, ?, ?, ?, ?, ?)`,
		"p1", "mid", "sid", 10, 20, "part-data")
	if err != nil {
		t.Fatal(err)
	}

	parts, err := queryParts(db, "sid")
	if err != nil {
		t.Fatalf("queryParts: %v", err)
	}
	if len(parts) != 1 || parts[0].ID != "p1" || parts[0].Data != "part-data" {
		t.Fatalf("unexpected parts: %+v", parts)
	}
}

func TestQueryTodos(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	_, err := db.Exec(`INSERT INTO session (id, project_id, directory, version, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		"sid", "p", "/d", "1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO todo (session_id, content, status, priority, position, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"sid", "fix bug", "pending", "high", 1, 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	todos, err := queryTodos(db, "sid")
	if err != nil {
		t.Fatalf("queryTodos: %v", err)
	}
	if len(todos) != 1 {
		t.Fatalf("expected 1 todo, got %d", len(todos))
	}
	if todos[0].Content != "fix bug" || todos[0].Position != 1 {
		t.Fatalf("unexpected todo: %+v", todos[0])
	}
	if todos[0].Status == nil || *todos[0].Status != "pending" {
		t.Fatalf("unexpected todo status: %+v", todos[0].Status)
	}
	if todos[0].Priority == nil || *todos[0].Priority != "high" {
		t.Fatalf("unexpected todo priority: %+v", todos[0].Priority)
	}
}

func TestQueryEvents(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	_, err := db.Exec(`INSERT INTO event (id, aggregate_id, seq, type, data) VALUES (?, ?, ?, ?, ?)`,
		"e1", "agg-1", 1, "created", `{"x": 1}`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO event (id, aggregate_id, seq, type, data) VALUES (?, ?, ?, ?, ?)`,
		"e2", "agg-1", 2, "updated", `{"x": 2}`)
	if err != nil {
		t.Fatal(err)
	}
	// Event for a different aggregate — should not appear
	_, err = db.Exec(`INSERT INTO event (id, aggregate_id, seq, type, data) VALUES (?, ?, ?, ?, ?)`,
		"e3", "other", 1, "created", `{}`)
	if err != nil {
		t.Fatal(err)
	}

	events, err := queryEvents(db, "agg-1")
	if err != nil {
		t.Fatalf("queryEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].ID != "e1" || events[0].Seq != 1 || events[0].Type != "created" {
		t.Fatalf("unexpected event[0]: %+v", events[0])
	}
	if events[1].ID != "e2" || events[1].Seq != 2 || events[1].Type != "updated" {
		t.Fatalf("unexpected event[1]: %+v", events[1])
	}
}

func TestQuerySessionMessages(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	_, err := db.Exec(`INSERT INTO session (id, project_id, directory, version, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		"sid", "p", "/d", "1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO session_message (id, session_id, type, time_created, time_updated, data, seq) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"sm1", "sid", "user", 10, 20, `{"prompt": "hi"}`, 1)
	if err != nil {
		t.Fatal(err)
	}

	sms, err := querySessionMessages(db, "sid")
	if err != nil {
		t.Fatalf("querySessionMessages: %v", err)
	}
	if len(sms) != 1 || sms[0].ID != "sm1" || sms[0].Type != "user" || sms[0].Seq != 1 {
		t.Fatalf("unexpected session_message: %+v", sms[0])
	}
}

func TestQuerySessionInputs(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	_, err := db.Exec(`INSERT INTO session (id, project_id, directory, version, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		"sid", "p", "/d", "1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO session_input (id, session_id, prompt, delivery, admitted_seq, promoted_seq, time_created) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"si1", "sid", "hello", "text", 1, 2, 100)
	if err != nil {
		t.Fatal(err)
	}

	sis, err := querySessionInputs(db, "sid")
	if err != nil {
		t.Fatalf("querySessionInputs: %v", err)
	}
	if len(sis) != 1 || sis[0].ID != "si1" || sis[0].Prompt != "hello" {
		t.Fatalf("unexpected session_input: %+v", sis[0])
	}
	if sis[0].AdmittedSeq == nil || *sis[0].AdmittedSeq != 1 {
		t.Fatalf("unexpected admitted_seq: %+v", sis[0].AdmittedSeq)
	}
	if sis[0].PromotedSeq == nil || *sis[0].PromotedSeq != 2 {
		t.Fatalf("unexpected promoted_seq: %+v", sis[0].PromotedSeq)
	}
}

func TestQuerySessionContextEpochs(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	_, err := db.Exec(`INSERT INTO session (id, project_id, directory, version, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		"sid", "p", "/d", "1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO session_context_epoch (session_id, baseline, snapshot, baseline_seq) VALUES (?, ?, ?, ?)`,
		"sid", "base-v1", "snap-v1", 42)
	if err != nil {
		t.Fatal(err)
	}

	epochs, err := querySessionContextEpochs(db, "sid")
	if err != nil {
		t.Fatalf("querySessionContextEpochs: %v", err)
	}
	if len(epochs) != 1 || epochs[0].Baseline != "base-v1" || epochs[0].BaselineSeq != 42 {
		t.Fatalf("unexpected epoch: %+v", epochs[0])
	}
}

func TestQueryMachineRows(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	// Insert data into machine tables
	_, err := db.Exec(`INSERT INTO credential (id, integration_id, label, value, active, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"cred1", "github", "token", "ghp_abc", 1, 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`INSERT INTO account (id, email, url, access_token, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		"acc1", "user@x.com", "https://x.com", "tok1", 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`INSERT INTO account_state (id, active_account_id) VALUES (?, ?)`,
		1, "acc1")
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`INSERT INTO control_account (email, url, access_token, active, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		"admin@x.com", "https://x.com", "admintok", 1, 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`INSERT INTO permission (id, project_id, action, resource, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		"perm1", "p1", "read", "session", 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	rows, err := queryMachineRows(db)
	if err != nil {
		t.Fatalf("queryMachineRows: %v", err)
	}

	expectedTables := []string{"credential", "account", "account_state", "control_account", "permission"}
	for _, table := range expectedTables {
		vals, ok := rows[table]
		if !ok {
			t.Fatalf("missing table %q in machine rows", table)
		}
		if len(vals) != 1 {
			t.Fatalf("expected 1 row in %q, got %d", table, len(vals))
		}
	}

	// Verify types in the result
	if _, ok := rows["credential"][0].(CredentialRow); !ok {
		t.Fatalf("credential row is not CredentialRow: %T", rows["credential"][0])
	}
	if _, ok := rows["account"][0].(AccountRow); !ok {
		t.Fatalf("account row is not AccountRow: %T", rows["account"][0])
	}
	if _, ok := rows["account_state"][0].(AccountStateRow); !ok {
		t.Fatalf("account_state row is not AccountStateRow: %T", rows["account_state"][0])
	}
	if _, ok := rows["control_account"][0].(ControlAccountRow); !ok {
		t.Fatalf("control_account row is not ControlAccountRow: %T", rows["control_account"][0])
	}
	if _, ok := rows["permission"][0].(PermissionRow); !ok {
		t.Fatalf("permission row is not PermissionRow: %T", rows["permission"][0])
	}
}

func TestQuerySessions_allColsNullable(t *testing.T) {
	// Verify nullable columns scan correctly when they contain NULL
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	_, err := db.Exec(`INSERT INTO session (id, project_id, directory, version, time_created, time_updated) VALUES (?, ?, ?, ?, ?, ?)`,
		"null-test", "p1", "/d", "1", 100, 200)
	if err != nil {
		t.Fatal(err)
	}

	sessions, err := querySessions(db)
	if err != nil {
		t.Fatalf("querySessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	r := sessions[0]
	// Non-nullable fields
	if r.ID != "null-test" || r.ProjectID != "p1" || r.Directory != "/d" || r.Version != "1" {
		t.Fatalf("unexpected session: %+v", r)
	}

	// All nullable fields should be nil
	nullablePointers := []struct {
		name string
		val  interface{}
	}{
		{"Title", r.Title}, {"Slug", r.Slug}, {"Permission", r.Permission},
		{"TimeCompacting", r.TimeCompacting}, {"TimeArchived", r.TimeArchived},
		{"WorkspaceID", r.WorkspaceID}, {"Path", r.Path}, {"Agent", r.Agent},
		{"Model", r.Model}, {"Cost", r.Cost},
		{"TokensInput", r.TokensInput}, {"TokensOutput", r.TokensOutput},
		{"TokensReasoning", r.TokensReasoning},
		{"TokensCacheRead", r.TokensCacheRead},
		{"TokensCacheWrite", r.TokensCacheWrite},
		{"Metadata", r.Metadata},
		{"SummaryAdditions", r.SummaryAdditions},
		{"SummaryDeletions", r.SummaryDeletions},
		{"SummaryFiles", r.SummaryFiles},
		{"SummaryDiffs", r.SummaryDiffs}, {"Revert", r.Revert},
		{"ParentID", r.ParentID},
	}
	for _, p := range nullablePointers {
		if !isNil(p.val) {
			t.Errorf("nullable field %s should be nil, got %v", p.name, p.val)
		}
	}
}

func TestQuerySessions_withNullableValues(t *testing.T) {
	db, path := setupTestDB(t)
	defer db.Close()
	defer os.Remove(path)

	title := "my session"
	slug := "my-slug"
	perm := "write"
	compacting := int64(500)
	archived := int64(600)
	wsID := "ws-1"
	sessPath := "/some/path"
	agent := "claude"
	model := "claude-4"
	cost := 0.05
	tokIn := int64(100)
	tokOut := int64(200)
	tokReason := int64(10)
	tokCacheR := int64(300)
	tokCacheW := int64(400)
	meta := `{"key": "val"}`
	sumAdd := int64(10)
	sumDel := int64(5)
	sumFiles := int64(3)
	sumDiffs := "diff content"
	revert := "revert-data"
	parentID := "parent-1"

	_, err := db.Exec(`INSERT INTO session (
		id, project_id, directory, title, slug, version, permission,
		time_created, time_updated, time_compacting, time_archived,
		workspace_id, path, agent, model, cost,
		tokens_input, tokens_output, tokens_reasoning,
		tokens_cache_read, tokens_cache_write,
		metadata, summary_additions, summary_deletions, summary_files,
		summary_diffs, revert, parent_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"full-id", "proj", "/dir", title, slug, "2", perm,
		100, 200, compacting, archived,
		wsID, sessPath, agent, model, cost,
		tokIn, tokOut, tokReason, tokCacheR, tokCacheW,
		meta, sumAdd, sumDel, sumFiles, sumDiffs, revert, parentID)
	if err != nil {
		t.Fatal(err)
	}

	sessions, err := querySessions(db)
	if err != nil {
		t.Fatalf("querySessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	r := sessions[0]
	if r.ID != "full-id" {
		t.Errorf("ID = %q, want full-id", r.ID)
	}
	if *r.Title != title {
		t.Errorf("Title = %q, want %q", *r.Title, title)
	}
	if *r.Slug != slug {
		t.Errorf("Slug = %q, want %q", *r.Slug, slug)
	}
	if *r.Permission != perm {
		t.Errorf("Permission = %q, want %q", *r.Permission, perm)
	}
	if *r.TimeCompacting != compacting {
		t.Errorf("TimeCompacting = %d, want %d", *r.TimeCompacting, compacting)
	}
	if *r.TimeArchived != archived {
		t.Errorf("TimeArchived = %d, want %d", *r.TimeArchived, archived)
	}
	if *r.WorkspaceID != wsID {
		t.Errorf("WorkspaceID = %q, want %q", *r.WorkspaceID, wsID)
	}
	if *r.Path != sessPath {
		t.Errorf("Path = %q, want %q", *r.Path, sessPath)
	}
	if *r.Agent != agent {
		t.Errorf("Agent = %q, want %q", *r.Agent, agent)
	}
	if *r.Model != model {
		t.Errorf("Model = %q, want %q", *r.Model, model)
	}
	if *r.Cost != cost {
		t.Errorf("Cost = %f, want %f", *r.Cost, cost)
	}
	if *r.TokensInput != tokIn {
		t.Errorf("TokensInput = %d, want %d", *r.TokensInput, tokIn)
	}
	if *r.TokensOutput != tokOut {
		t.Errorf("TokensOutput = %d, want %d", *r.TokensOutput, tokOut)
	}
	if *r.TokensReasoning != tokReason {
		t.Errorf("TokensReasoning = %d, want %d", *r.TokensReasoning, tokReason)
	}
	if *r.TokensCacheRead != tokCacheR {
		t.Errorf("TokensCacheRead = %d, want %d", *r.TokensCacheRead, tokCacheR)
	}
	if *r.TokensCacheWrite != tokCacheW {
		t.Errorf("TokensCacheWrite = %d, want %d", *r.TokensCacheWrite, tokCacheW)
	}
	if *r.Metadata != meta {
		t.Errorf("Metadata = %q, want %q", *r.Metadata, meta)
	}
	if *r.SummaryAdditions != sumAdd {
		t.Errorf("SummaryAdditions = %d, want %d", *r.SummaryAdditions, sumAdd)
	}
	if *r.SummaryDeletions != sumDel {
		t.Errorf("SummaryDeletions = %d, want %d", *r.SummaryDeletions, sumDel)
	}
	if *r.SummaryFiles != sumFiles {
		t.Errorf("SummaryFiles = %d, want %d", *r.SummaryFiles, sumFiles)
	}
	if *r.SummaryDiffs != sumDiffs {
		t.Errorf("SummaryDiffs = %q, want %q", *r.SummaryDiffs, sumDiffs)
	}
	if *r.Revert != revert {
		t.Errorf("Revert = %q, want %q", *r.Revert, revert)
	}
	if *r.ParentID != parentID {
		t.Errorf("ParentID = %q, want %q", *r.ParentID, parentID)
	}
}

func TestToAnySlice(t *testing.T) {
	ints := []int{1, 2, 3}
	got := toAnySlice(ints)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, v := range got {
		if v.(int) != ints[i] {
			t.Errorf("got[%d] = %d, want %d", i, v, ints[i])
		}
	}
}

// isNil checks if a pointer interface value is nil.
func isNil(v interface{}) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Ptr && rv.IsNil()
}
