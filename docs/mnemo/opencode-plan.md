# OpenCode Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend Mnemo to sync OpenCode sessions (alongside Claude sessions) in a single `mnemo push`/`pull`/`sync` run.

**Architecture:** A new `internal/opencode` package handles DB snapshot, per-session JSONL export, staging-tree write, JSONL→DB replay, and identity-based path remapping. The existing `command/push.go`, `command/pull.go`, and `command/sync.go` each add a single call to the new package alongside the existing Claude stage/restore steps. Staging tree gets a new `opencode-sessions/` subdirectory under each `by-id/<identity>/`.

**Tech Stack:** Go 1.23, `database/sql` + `github.com/mattn/go-sqlite3`, SQLite `.backup` command via `sqlite3` binary.

## Global Constraints

- Extend existing Mnemo functions — never modify existing Claude stage/restore/sync code paths
- OpenCode DB is discovered at configurable path (default `~/.local/share/opencode/opencode.db`)
- Missing OpenCode DB is non-fatal: push skips silently, pull warns and skips
- All UUID primary keys use `INSERT OR REPLACE` for idempotent replay
- All timestamps in JSONL data are verbatim from source DB — no `time.Now()` during export or replay
- Path columns (`session.directory`, `project.worktree`) are remapped via identity resolution during restore

---

## File Structure

```
internal/opencode/
  export.go       — SnapshotDB, ExportSession, writeProjectManifest
  replay.go       — ReplaySession, ReplayMachineRows, recomputeSeq
  stage.go        — Stage: snapshot → iterate sessions → export → write staging tree
  restore.go      — Restore: walk staging tree → resolve identity → replay
  opencode.go     — OpenDB, identityFromDirectory, row types, errors
  opencode_test.go
```

Interfaces exposed to `internal/command`:

```go
func Stage(dbPath, stageRoot, encHome string) (int, error)
func Restore(restoredRoot, dbPath, host, encHome string, man *manifest.Manifest) error
```

---

### Task 1: internal/opencode package — DB helpers and snapshot

**Files:**
- Create: `internal/opencode/opencode.go` — package, errors, DB open/snapshot, identity resolution, row types, query helpers
- Test: `internal/opencode/opencode_test.go`

**Interfaces:**
- Produces: `OpenDBReadOnly`, `SnapshotDB`, `IdentifyFromDirectory`, exported row types used by Task 2

- [ ] **Step 1: Define package and error defs**

```go
package opencode

import "fmt"

var ErrDBNotFound = fmt.Errorf("opencode database not found")
```

- [ ] **Step 2: Write OpenDB helper**

```go
import (
    "database/sql"
    "os"
    _ "github.com/mattn/go-sqlite3"
)

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
```

- [ ] **Step 3: Write DB snapshot helper**

```go
import "os/exec"

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
```

- [ ] **Step 4: Write identity-from-directory**

```go
import "github.com/ekinertac/mnemo/internal/identity"

func IdentifyFromDirectory(dir, encHome string) identity.Identity {
    return identity.FromEncoded(identity.Encode(dir), encHome)
}
```

- [ ] **Step 5: Write exported row types**

One struct per exported table. For the session table:

```go
type SessionRow struct {
    ID        string
    ProjectID string
    Directory string
    Title     string
    TimeCreated int64
    TimeUpdated int64
    // plus all other columns from the session table schema
}
```

And similarly for: `MessageRow`, `PartRow`, `TodoRow`, `EventRow`, `SessionMessageRow`, `SessionInputRow`, `SessionContextEpochRow`, `EventSequenceRow`, `ProjectRow`, `CredentialRow`, `AccountRow`, `AccountStateRow`, `ControlAccountRow`, `PermissionRow`.

All fields match the SQLite column types: `string` for TEXT, `int64` for INTEGER, `sql.NullString`/`*string` for nullable TEXT columns.

- [ ] **Step 6: Write query helpers**

One query function per child table, filtering by session_id:

```go
func querySessions(db *sql.DB) ([]SessionRow, error) { … }
func queryMessages(db *sql.DB, sessionID string) ([]MessageRow, error) { … }
func queryParts(db *sql.DB, sessionID string) ([]PartRow, error) { … }
func queryTodos(db *sql.DB, sessionID string) ([]TodoRow, error) { … }
func queryEvents(db *sql.DB, sessionID string) ([]EventRow, error) { … }
func querySessionMessages(db *sql.DB, sessionID string) ([]SessionMessageRow, error) { … }
func querySessionInputs(db *sql.DB, sessionID string) ([]SessionInputRow, error) { … }
func querySessionContextEpochs(db *sql.DB, sessionID string) ([]SessionContextEpochRow, error) { … }
func queryMachineRows(db *sql.DB) (map[string][]any, error) { … } // credential, account, etc.
```

Each scans rows directly into the typed struct using `rows.Scan`.

- [ ] **Step 7: Add sqlite3 driver**

```bash
go get github.com/mattn/go-sqlite3
```

- [ ] **Step 8: Compile check**

```bash
go build ./internal/opencode/
```
Expected: no errors

- [ ] **Step 9: Commit**

```bash
git add internal/opencode/opencode.go internal/opencode/opencode_test.go go.mod go.sum
git commit -m "feat(opencode): DB helpers, snapshot, identity resolution, row types"
```

---

### Task 2: Export session → JSONL

**Files:**
- Create: `internal/opencode/export.go` — `ExportSession`, `ExportMachineRows`
- Test: `internal/opencode/export_test.go`

**Interfaces:**
- Consumes: row types from Task 1
- Produces: `ExportSession(*sql.DB, string) ([]byte, error)` — JSONL bytes for one session

- [ ] **Step 1: Write the row→JSONL line encoder**

```go
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
```

- [ ] **Step 2: Write row→map helper**

```go
func rowToMap(s interface{}) (map[string]any, error) { … }
```

Uses reflection or manual field-to-map conversion for each row type. For simplicity, implement per-type converters (`sessionToMap`, `messageToMap`, etc.) that produce the map directly from the struct fields, matching JSON column names to SQL column names.

- [ ] **Step 3: Write ExportSession**

```go
func ExportSession(snap *sql.DB, sessionID string) ([]byte, error) {
    var lines [][]byte

    // Session row
    s, err := querySession(snap, sessionID)
    lines = append(lines, lineFromRow("session", s.ID, sessionToMap(*s), s.TimeUpdated))

    // Messages
    msgs, _ := queryMessages(snap, sessionID)
    for _, m := range msgs {
        lines = append(lines, lineFromRow("message", m.ID, messageToMap(m), m.TimeUpdated))
    }

    // Parts
    parts, _ := queryParts(snap, sessionID)
    for _, p := range parts {
        lines = append(lines, lineFromRow("part", p.ID, partToMap(p), p.TimeUpdated))
    }

    // Todos — composite PK "sessionID|position"
    todos, _ := queryTodos(snap, sessionID)
    for _, t := range todos {
        pk := t.SessionID + "|" + strconv.Itoa(t.Position)
        lines = append(lines, lineFromRow("todo", pk, todoToMap(t), t.TimeUpdated))
    }

    // Events
    events, _ := queryEvents(snap, sessionID)
    for _, e := range events {
        lines = append(lines, lineFromRow("event", e.ID, eventToMap(e), e.TimeUpdated))
    }

    // session_message, session_input, session_context_epoch
    // …

    // Sort by timestamp (epoch ms)
    sort.Slice(lines, func(i, j int) bool {
        return extractTimestamp(lines[i]) < extractTimestamp(lines[j])
    })

    return bytes.Join(lines, []byte("\n")) + []byte("\n"), nil
}
```

- [ ] **Step 4: Write ExportMachineRows**

```go
func ExportMachineRows(snap *sql.DB) ([]byte, error) {
    var lines [][]byte
    // credential, account, account_state, control_account, permission, project
    // each as lineFromRow
    …
    sort.Slice(lines, func(i, j int) bool {
        return extractTimestamp(lines[i]) < extractTimestamp(lines[j])
    })
    return bytes.Join(lines, []byte("\n")) + []byte("\n"), nil
}
```

- [ ] **Step 5: Write tests**

```go
func TestExportSession(t *testing.T) {
    // 1. Create in-memory SQLite DB with schema
    // 2. Insert one session + 2 messages + 2 parts + 1 todo + 1 event
    // 3. Call ExportSession
    // 4. Verify output has 7 lines (1 session + 2 messages + 2 parts + 1 todo + 1 event)
    // 5. Verify each line is valid JSON with expected fields
    // 6. Verify lines are sorted by timestamp
}
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/opencode/ -run TestExport -v
```

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/opencode/export.go internal/opencode/export_test.go
git commit -m "feat(opencode): export session and machine rows to JSONL"
```

---

### Task 3: Stage — write sessions to staging tree

**Files:**
- Create: `internal/opencode/stage.go` — `Stage` entry point
- Test: `internal/opencode/stage_test.go`

**Interfaces:**
- Consumes: `SnapshotDB`, `ExportSession`, `ExportMachineRows`, `IdentifyFromDirectory`
- Produces: `Stage(dbPath, stageRoot, encHome string) (int, error)`

- [ ] **Step 1: Write Stage function**

```go
func Stage(dbPath, stageRoot, encHome string) (int, error) {
    // 1. Check DB exists
    if _, err := os.Stat(dbPath); os.IsNotExist(err) {
        return 0, nil // skip silently
    }

    // 2. Snapshot
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

    // 3. Query all sessions
    sessions, err := querySessions(db)
    if err != nil {
        return 0, fmt.Errorf("query sessions: %w", err)
    }
    if len(sessions) == 0 {
        return 0, nil
    }

    // 4. Group by identity
    byIdentity := map[identity.Identity][]SessionRow{}
    for _, s := range sessions {
        dir := s.Directory
        if dir == "" || s.ProjectID == "global" {
            dir = "/" // global project — identity from root
        }
        id := IdentifyFromDirectory(dir, encHome)
        byIdentity[id] = append(byIdentity[id], s)
    }

    // 5. Export per session
    for id, sessions := range byIdentity {
        idSafe := identity.PathSafe(id)
        baseDir := filepath.Join(stageRoot, "by-id", idSafe, "opencode-sessions")
        if err := os.MkdirAll(baseDir, 0755); err != nil {
            return 0, err
        }
        for _, s := range sessions {
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

    // 6. Machine rows
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
```

- [ ] **Step 2: Write test**

```go
func TestStage(t *testing.T) {
    // 1. Create temp SQLite DB with known session data
    // 2. Call Stage(dbPath, tmpStageRoot, encHome)
    // 3. Verify by-id/<identity>/opencode-sessions/<session-id>.jsonl exists
    // 4. Verify opencode-machine.jsonl exists
    // 5. Verify each file contains valid JSONL
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/opencode/ -run TestStage -v
```

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/opencode/stage.go internal/opencode/stage_test.go
git commit -m "feat(opencode): Stage writes per-session JSONL to staging tree"
```

---

### Task 4: Replay — read merged JSONL into local DB

**Files:**
- Create: `internal/opencode/replay.go` — `ReplaySession`, `ReplayMachineRows`, `RecomputeEventSequence`
- Test: `internal/opencode/replay_test.go`

**Interfaces:**
- Consumes: merged JSONL bytes
- Produces: `ReplaySession(*sql.DB, []byte, identity.Identity, string) error`

- [ ] **Step 1: Write JSONL line parser**

```go
type RowEvent struct {
    Type      string         `json:"type"`
    Table     string         `json:"table"`
    ID        string         `json:"id"`
    Data      map[string]any `json:"data"`
    Timestamp int64          `json:"timestamp"`
}

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
```

- [ ] **Step 2: Write upsert builder**

```go
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
```

- [ ] **Step 3: Write path remapping**

```go
func remapPaths(data map[string]any, localEncoded string) {
    for _, col := range []string{"directory", "worktree"} {
        if v, ok := data[col].(string); ok && v != "" {
            // The source path is encoded (Claude-encoded). The localEncoded is
            // this machine's encoded form of the same home-relative path.
            // We check if the source starts with the encoded-home prefix portion
            // and replace that portion with the local encoded directory.
            // Since identity is home:-Code-foo, both source and local path
            // share the same tail "-Code-foo" — only the home prefix differs.
            data[col] = replaceEncodedPrefix(v, localEncoded)
        }
    }
}

// replaceEncodedPrefix replaces any leading encoded-path segment that looks like
// a home-based path with the local encoded path.
func replaceEncodedPrefix(encoded, localEncoded string) string { … }
```

- [ ] **Step 4: Write ReplaySession**

```go
func ReplaySession(db *sql.DB, data []byte, id identity.Identity, encHome string) error {
    localEncoded, ok := identity.ToEncoded(id, encHome)
    if !ok {
        return fmt.Errorf("cannot resolve identity %s", id)
    }

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
            remapPaths(ev.Data, localEncoded)
        }
        if err := upsertRow(db, ev); err != nil {
            return fmt.Errorf("upsert %s.%s: %w", ev.Table, ev.ID, err)
        }
    }
    return nil
}
```

- [ ] **Step 5: Write ReplayMachineRows**

```go
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
```

- [ ] **Step 6: Write RecomputeEventSequence**

```go
func RecomputeEventSequence(db *sql.DB) error {
    _, err := db.Exec(`
        INSERT OR REPLACE INTO event_sequence (aggregate_id, seq)
        SELECT aggregate_id, MAX(seq) FROM event GROUP BY aggregate_id
    `)
    return err
}
```

- [ ] **Step 7: Write tests**

```go
func TestReplaySession(t *testing.T) {
    // 1. Create in-memory SQLite DB with session/message/part tables
    // 2. Build JSONL by hand (one session, one message, one part)
    // 3. Call ReplaySession
    // 4. Verify rows exist in DB with correct column values
    // 5. Call ReplaySession again with same data (idempotent — no error, no duplicates)
}

func TestPathRemap(t *testing.T) {
    // Verify path prefix replacement works correctly
}
```

- [ ] **Step 8: Run tests**

```bash
go test ./internal/opencode/ -run TestReplay -v
```

Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/opencode/replay.go internal/opencode/replay_test.go
git commit -m "feat(opencode): replay merged JSONL into local DB with path remapping"
```

---

### Task 5: Restore — walk staging tree, resolve identity, replay

**Files:**
- Create: `internal/opencode/restore.go` — `Restore` entry point
- Test: `internal/opencode/restore_test.go`

**Interfaces:**
- Consumes: `ReplaySession`, `ReplayMachineRows`, `RecomputeEventSequence`
- Produces: `Restore(restoredRoot, dbPath, host, encHome string, man *manifest.Manifest) error`

- [ ] **Step 1: Write Restore function**

```go
func Restore(restoredRoot, dbPath, host, encHome string, man *manifest.Manifest) error {
    // 1. Open local DB
    db, err := sql.Open("sqlite3", dbPath)
    if err != nil {
        if os.IsNotExist(errors.Unwrap(err)) {
            fmt.Fprintf(os.Stderr, "mnemo: warning: opencode db not found at %s — skipping restore\n", dbPath)
            return nil
        }
        return fmt.Errorf("open local opencode db: %w", err)
    }
    defer db.Close()

    // 2. Walk by-id/<identity>/opencode-sessions/
    byIDRoot := filepath.Join(restoredRoot, "by-id")
    idDirs, err := os.ReadDir(byIDRoot)
    if err != nil {
        if os.IsNotExist(err) {
            return nil // no opencode sessions in this snapshot
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
            if err := ReplaySession(db, data, id, encHome); err != nil {
                return fmt.Errorf("replay %s: %w", f.Name(), err)
            }
            count++
        }
    }

    // 3. Machine rows
    machineFile := filepath.Join(restoredRoot, "opencode-machine.jsonl")
    if data, err := os.ReadFile(machineFile); err == nil {
        if err := ReplayMachineRows(db, data); err != nil {
            return fmt.Errorf("replay machine rows: %w", err)
        }
    }

    // 4. Recompute event_sequence
    if err := RecomputeEventSequence(db); err != nil {
        return fmt.Errorf("recompute event_sequence: %w", err)
    }

    if count > 0 {
        fmt.Printf("mnemo: restored %d opencode sessions\n", count)
    }
    return nil
}
```

- [ ] **Step 2: Write test**

```go
func TestRestore(t *testing.T) {
    // 1. Create temp opencode DB
    // 2. Create staging tree with by-id/<identity>/opencode-sessions/<session-id>.jsonl
    // 3. Call Restore
    // 4. Verify data in local DB matches expectations
    // 5. Verify event_sequence is correct
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/opencode/ -run TestRestore -v
```

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/opencode/restore.go internal/opencode/restore_test.go
git commit -m "feat(opencode): Restore walks staging tree and replays sessions into local DB"
```

---

### Task 6: Wire Stage into push command

**Files:**
- Modify: `internal/command/push.go`

- [ ] **Step 1: Add import and default DB path helper to push.go**

```go
import "github.com/ekinertac/mnemo/internal/opencode"

func defaultOpenCodeDB() (string, error) {
    home, err := os.UserHomeDir()
    if err != nil {
        return "", err
    }
    return filepath.Join(home, ".local", "share", "opencode", "opencode.db"), nil
}
```

- [ ] **Step 2: Add opencode stage call in runPush**

After the Claude `stage.Build` call and its result reporting, add:

```go
ocDB, err := defaultOpenCodeDB()
if err != nil {
    return err
}
ocCount, err := opencode.Stage(ocDB, stageRoot, identity.EncodedHome(home))
if err != nil {
    fmt.Fprintf(os.Stderr, "mnemo: warning: opencode stage: %v\n", err)
} else if ocCount > 0 {
    fmt.Printf("mnemo: staged %d opencode sessions\n", ocCount)
}
```

- [ ] **Step 3: Build check**

```bash
go build ./...
```
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add internal/command/push.go
git commit -m "feat(push): stage opencode sessions alongside Claude sessions"
```

---

### Task 7: Wire Restore into pull command

**Files:**
- Modify: `internal/command/pull.go`

- [ ] **Step 1: Add import in pull.go**

```go
import "github.com/ekinertac/mnemo/internal/opencode"
```

- [ ] **Step 2: Add opencode restore call in runPull**

After the Claude lay-down and its unmapped reporting (`len(rep.Unmapped)` block), add:

```go
ocDB, err := defaultOpenCodeDB()
if err != nil {
    return err
}
if err := opencode.Restore(target, ocDB, host, identity.EncodedHome(home), man); err != nil {
    fmt.Fprintf(os.Stderr, "mnemo: warning: opencode restore: %v\n", err)
}
```

Also add the `defaultOpenCodeDB` helper if not already present (it may be shared from push.go — either duplicate the helper in pull.go, or extract to root.go).

- [ ] **Step 3: Build check**

```bash
go build ./...
```
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add internal/command/pull.go
git commit -m "feat(pull): restore opencode sessions alongside Claude sessions"
```

---

### Task 8: Wire into sync command

**Files:**
- Modify: `internal/command/sync.go`

- [ ] **Step 1: Add import and restore call in runSync**

After the Claude `restore.LayDown` call and its reporting, before the push step:

```go
ocDB, err := defaultOpenCodeDB()
if err != nil {
    return err
}
if err := opencode.Restore(target, ocDB, host, identity.EncodedHome(home), man); err != nil {
    fmt.Fprintf(os.Stderr, "mnemo: warning: opencode restore: %v\n", err)
}
```

The subsequent `runPush` already handles staging opencode sessions (from Task 6), so `sync` completes the full cycle: restore opencode → merge → push opencode.

- [ ] **Step 2: Build check**

```bash
go build ./...
```
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/command/sync.go
git commit -m "feat(sync): restore and re-push opencode sessions"
```

---

### Task 9: Integration test with real opencode DB

**Files:**
- Create: `internal/command/oc_e2e_test.go` (build tag `e2e`)

- [ ] **Step 1: Write e2e test**

```go
//go:build e2e

package command

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/ekinertac/mnemo/internal/identity"
    "github.com/ekinertac/mnemo/internal/opencode"
)

func TestOpenCodeStageRoundTrip(t *testing.T) {
    home, _ := os.UserHomeDir()
    dbPath := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
    if _, err := os.Stat(dbPath); os.IsNotExist(err) {
        t.Skip("no opencode DB on this machine")
    }

    encHome := identity.EncodedHome(home)
    stageRoot := t.TempDir()
    targetDB := filepath.Join(t.TempDir(), "opencode.db")

    // Stage
    count, err := opencode.Stage(dbPath, stageRoot, encHome)
    if err != nil {
        t.Fatal(err)
    }
    if count == 0 {
        t.Fatal("expected at least 1 session")
    }

    // Verify staging tree structure
    byID, _ := os.ReadDir(filepath.Join(stageRoot, "by-id"))
    if len(byID) == 0 {
        t.Fatal("expected by-id/ directories")
    }
    found := 0
    for _, idDir := range byID {
        sessDir := filepath.Join(stageRoot, "by-id", idDir.Name(), "opencode-sessions")
        files, err := os.ReadDir(sessDir)
        if err == nil {
            found += len(files)
        }
    }
    if found != count {
        t.Fatalf("expected %d session files in staging tree, got %d", count, found)
    }

    // Verify machine rows exported
    if _, err := os.Stat(filepath.Join(stageRoot, "opencode-machine.jsonl")); os.IsNotExist(err) {
        t.Log("no opencode-machine.jsonl (expected on empty credential tables)")
    }

    t.Logf("opencode stage: %d sessions staged successfully", count)
}
```

- [ ] **Step 2: Run e2e test**

```bash
go test -tags=e2e ./internal/command/ -run TestOpenCode -v
```

Expected: PASS (or SKIP if no opencode DB on this machine)

- [ ] **Step 3: Commit**

```bash
git add internal/command/oc_e2e_test.go
git commit -m "test: e2e test for opencode stage round trip"
```
