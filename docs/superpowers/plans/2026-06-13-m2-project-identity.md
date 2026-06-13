# M2 — Project Identity & Resume-Aware Restore Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a Claude session pushed from one machine resume on another, keyed on a path-tokenized project identity instead of the machine-specific encoded cwd.

**Architecture:** Identity is a pure function over Claude Code's *encoded* cwd string (the `projects/<encoded>` dir name), never a decoded filesystem path — because Claude's encoding (`[^A-Za-z0-9] → -`) is irreversibly lossy. Push stages project sessions under `by-id/<identity>/…`; pull resolves each identity back to this machine's encoded cwd (de-tokenizing the home prefix, or applying a `mnemo map` override) and lays the transcript down where `claude --resume` expects it. A slim `projects.json` in the repo holds overrides + machine bookkeeping.

**Tech Stack:** Go (stdlib only), restic via `internal/restic`, builds on M1's `internal/filter` + `internal/stage`.

**Step-0 findings (already done — these are facts, not assumptions):**
- Claude encodes a cwd by replacing every non-alphanumeric character with `-`, preserving case. Evidence: `/Users/ekinertac/.dotfiles` → `-Users-ekinertac--dotfiles`; `…/Sublime Text/…` → `…-Sublime-Text-…`; `ChatHumble`/`OpenLogi` keep case. Lossy: `age.sh`, `age-sh`, `age sh` all encode to `age-sh`.
- A Windows path under the user profile encoded *without* the drive letter: `C:\Users\ekinertac\AppData\Roaming\…` → `-Users-ekinertac-AppData-Roaming-…`. So `C:\Users\<u>` and `/Users/<u>` both encode to `-Users-<u>`.
- Consequence: identity lives in encoded space; NFC normalization is unnecessary (non-ASCII already collapses to `-`); only case needs case-insensitive comparison.

---

## File Structure

- `internal/identity/identity.go` — Claude cwd encoding + identity tokenize/detokenize (pure).
- `internal/identity/identity_test.go` — tests.
- `internal/manifest/manifest.go` — `projects.json` typed load/save/override/bookkeeping.
- `internal/manifest/manifest_test.go` — tests.
- `internal/stage/stage.go` — MODIFY: add a `Mapper` so push can rewrite `projects/<enc>/…` → `by-id/<id>/…`.
- `internal/stage/stage_test.go` — MODIFY: pass `nil` mapper (identity) in existing tests; add a remap test.
- `internal/restore/restore.go` — resolve identity → local encoded dir; lay restored tree into `~/.claude`.
- `internal/restore/restore_test.go` — tests (incl. simulated cross-home).
- `internal/command/push.go` — MODIFY: build the project remap mapper from identity + manifest; write manifest.
- `internal/command/pull.go` — MODIFY: add resume-aware lay-down (keep `--target` dry-restore path).
- `internal/command/map.go` / `projects.go` / `machines.go` — new subcommands.
- `internal/command/root.go` — MODIFY: register the new subcommands; manifest path helper.
- `docs/DESIGN.md` + spec — MODIFY: align identity examples to the encoded form; mark step-0 resolved.
- `HANDOFF.md` — MODIFY: M2 status.

---

## Task 1: Claude cwd encoding + EncodedHome

**Files:**
- Create: `internal/identity/identity.go`
- Test: `internal/identity/identity_test.go`

- [ ] **Step 1: Write the failing test**

```go
package identity

import "testing"

func TestEncodeReplacesNonAlnumWithDash(t *testing.T) {
	cases := map[string]string{
		"/Users/ekinertac/Code/foo":     "-Users-ekinertac-Code-foo",
		"/Users/ekinertac/.dotfiles":    "-Users-ekinertac--dotfiles", // '/' and '.' both -> '-'
		"/Users/ekinertac/Code/age.sh":  "-Users-ekinertac-Code-age-sh",
		"ChatHumble":                    "ChatHumble", // case preserved
		"a b":                           "a-b",        // space -> '-'
		"café":                          "caf-",       // non-ASCII -> '-'
	}
	for in, want := range cases {
		if got := Encode(in); got != want {
			t.Errorf("Encode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEncodedHomeStripsWindowsDrive(t *testing.T) {
	// Unix home encodes directly; Windows %USERPROFILE% drops the drive (step-0 finding).
	if got := EncodedHome("/Users/ekinertac"); got != "-Users-ekinertac" {
		t.Errorf("unix EncodedHome = %q, want -Users-ekinertac", got)
	}
	if got := EncodedHome(`C:\Users\ekinertac`); got != "-Users-ekinertac" {
		t.Errorf("windows EncodedHome = %q, want -Users-ekinertac", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/identity/ -run TestEncode -v`
Expected: FAIL — build error, `undefined: Encode` / `undefined: EncodedHome`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package identity turns Claude Code's encoded project-dir names into machine-independent
// project identities and back. It is the heart of M2 (resume-aware cross-machine restore).
//
// Why it works purely on the ENCODED string (the `projects/<encoded>` dir name) and never on a
// decoded filesystem path: Claude encodes a cwd by replacing every non-alphanumeric character
// with '-' (verified against real data — see docs/superpowers/plans/2026-06-13-...md step-0),
// which is irreversibly lossy (`age.sh` and `age-sh` collapse to the same string). Decoding is
// therefore impossible in general; tokenizing the encoded string is not. Identity = the encoded
// cwd with the machine-specific encoded-home prefix replaced by a token.
//
// Related: internal/stage (uses this to key the staging tree), internal/restore (inverts it),
// docs/DESIGN.md §4.4.
package identity

import "strings"

// Identity is a machine-independent project key. Two forms:
//   home:<tail>   project under the user's home, tail is the encoded path below home (e.g.
//                 "home:-Code-foo"). The home prefix is tokenized away, so it matches across
//                 machines whose home-relative layout agrees, regardless of where home is.
//   abs:<encoded> project outside home; the literal encoded absolute path. Matches only when
//                 that encoded path is identical on both machines.
type Identity string

// Encode reproduces Claude Code's cwd-encoding: every non-[A-Za-z0-9] rune becomes '-', case
// preserved. This is the lossy mapping Claude itself uses for projects/<encoded> dir names.
func Encode(p string) string {
	var b strings.Builder
	b.Grow(len(p))
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// EncodedHome returns this machine's encoded-home prefix — Claude's encoding of $HOME. On
// Windows the drive letter is stripped first, because Claude encodes user-profile paths without
// it (step-0 finding: C:\Users\u and /Users/u both -> -Users-u). This is the one OS-specific
// seam and the single thing to confirm on a live Windows box.
func EncodedHome(home string) string {
	return Encode(stripWindowsDrive(home))
}

func stripWindowsDrive(p string) string {
	if len(p) >= 2 && p[1] == ':' &&
		((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) {
		return p[2:]
	}
	return p
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/identity/ -run 'TestEncode|TestEncodedHome' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/identity/identity.go internal/identity/identity_test.go
git commit -m "M2: Claude cwd encoding + EncodedHome (step-0 rule, encoded space)"
```

---

## Task 2: FromEncoded / ToEncoded (tokenize ⇄ detokenize)

**Files:**
- Modify: `internal/identity/identity.go`
- Test: `internal/identity/identity_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestFromEncodedHomeAndAbs(t *testing.T) {
	home := "-Users-ekinertac"
	if got := FromEncoded("-Users-ekinertac-Code-foo", home); got != "home:-Code-foo" {
		t.Errorf("under-home = %q, want home:-Code-foo", got)
	}
	if got := FromEncoded("-opt-services-bar", home); got != "abs:-opt-services-bar" {
		t.Errorf("outside-home = %q, want abs:-opt-services-bar", got)
	}
	// The home dir itself (session opened at $HOME) -> empty tail.
	if got := FromEncoded("-Users-ekinertac", home); got != "home:" {
		t.Errorf("home root = %q, want home:", got)
	}
}

func TestFromEncodedPrefixBoundary(t *testing.T) {
	// "-Users-ekin" must NOT be treated as a prefix of "-Users-ekinside".
	if got := FromEncoded("-Users-ekinside-x", "-Users-ekin"); got != "abs:-Users-ekinside-x" {
		t.Errorf("boundary leak = %q, want abs:-Users-ekinside-x", got)
	}
}

func TestFromEncodedCaseInsensitiveHome(t *testing.T) {
	// macOS/Windows are case-insensitive; a differently-cased home prefix still tokenizes.
	if got := FromEncoded("-USERS-ekinertac-Code-foo", "-Users-ekinertac"); got != "home:-Code-foo" {
		t.Errorf("case-insensitive home = %q, want home:-Code-foo", got)
	}
}

func TestToEncodedRoundTrip(t *testing.T) {
	home := "-Users-ekin" // a DIFFERENT machine's home prefix
	enc, ok := ToEncoded("home:-Code-foo", home)
	if !ok || enc != "-Users-ekin-Code-foo" {
		t.Errorf("ToEncoded(home) = %q,%v want -Users-ekin-Code-foo,true", enc, ok)
	}
	enc, ok = ToEncoded("abs:-opt-bar", home)
	if !ok || enc != "-opt-bar" {
		t.Errorf("ToEncoded(abs) = %q,%v want -opt-bar,true", enc, ok)
	}
	if _, ok := ToEncoded("garbage-no-scheme", home); ok {
		t.Error("malformed identity should return ok=false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/identity/ -run 'FromEncoded|ToEncoded' -v`
Expected: FAIL — `undefined: FromEncoded` / `undefined: ToEncoded`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/identity/identity.go`:

```go
// FromEncoded computes the identity for a Claude projects/<encodedCwd> dir name, given this
// machine's encodedHome prefix. Under-home dirs become home:<tail>; everything else abs:<enc>.
// Comparison is case-insensitive (case-insensitive filesystems), with a separator boundary so
// "-Users-ekin" does not swallow "-Users-ekinside".
func FromEncoded(encodedCwd, encodedHome string) Identity {
	if tail, ok := stripEncodedHome(encodedCwd, encodedHome); ok {
		return Identity("home:" + tail)
	}
	return Identity("abs:" + encodedCwd)
}

// stripEncodedHome returns the encoded tail below home (starting with '-', or "" at home root)
// when encodedCwd is encodedHome or sits beneath it; ok=false otherwise.
func stripEncodedHome(encodedCwd, encodedHome string) (string, bool) {
	n := len(encodedHome)
	if len(encodedCwd) < n || !strings.EqualFold(encodedCwd[:n], encodedHome) {
		return "", false
	}
	rest := encodedCwd[n:]
	if rest == "" {
		return "", true // session opened exactly at $HOME
	}
	if rest[0] != '-' {
		return "", false // prefix boundary: "-Users-ekin" vs "-Users-ekinside"
	}
	return rest, true
}

// ToEncoded inverts FromEncoded for THIS machine: home: identities get encodedHome prepended;
// abs: identities are used verbatim. ok=false for a malformed identity (no known scheme).
func ToEncoded(id Identity, encodedHome string) (string, bool) {
	s := string(id)
	if tail, ok := strings.CutPrefix(s, "home:"); ok {
		return encodedHome + tail, true
	}
	if enc, ok := strings.CutPrefix(s, "abs:"); ok {
		return enc, true
	}
	return "", false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/identity/ -v`
Expected: PASS (all identity tests).

- [ ] **Step 5: Commit**

```bash
git add internal/identity/identity.go internal/identity/identity_test.go
git commit -m "M2: identity tokenize/detokenize with prefix-boundary + case folding"
```

---

## Task 3: `projects.json` manifest

**Files:**
- Create: `internal/manifest/manifest.go`
- Test: `internal/manifest/manifest_test.go`

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/manifest/ -v`
Expected: FAIL — `undefined: Load`.

- [ ] **Step 3: Write minimal implementation**

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/manifest/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/manifest/
git commit -m "M2: slim projects.json manifest (overrides + machine bookkeeping)"
```

---

## Task 4: Identity-keyed staging (push side)

**Files:**
- Modify: `internal/stage/stage.go` (add `Mapper` param to `Build`)
- Modify: `internal/stage/stage_test.go` (existing calls pass `nil`; add remap test)

- [ ] **Step 1: Update existing tests to the new signature + add a remap test**

In `internal/stage/stage_test.go`, change both existing `Build(...)` calls to pass a `nil` mapper:

```go
res, err := Build(src, stageDir, filter.Classifier{}, nil)
```
```go
if _, err := Build(filepath.Join(t.TempDir(), "nope"), t.TempDir(), filter.Classifier{}, nil); err == nil {
```

Then add:

```go
// With a Mapper, durable paths are rewritten in the staging tree (M2 keys projects/ by identity).
func TestBuildAppliesMapper(t *testing.T) {
	src := t.TempDir()
	stageDir := t.TempDir()
	writeTree(t, src, map[string]string{
		"projects/-Users-ekinertac-Code-foo/s.jsonl": "s\n",
		"history.jsonl": "h\n",
	})
	mapper := func(rel string) string {
		if rel == filepath.FromSlash("projects/-Users-ekinertac-Code-foo/s.jsonl") {
			return filepath.FromSlash("by-id/home:-Code-foo/s.jsonl")
		}
		return rel
	}
	if _, err := Build(src, stageDir, filter.Classifier{}, mapper); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stageDir, filepath.FromSlash("by-id/home:-Code-foo/s.jsonl"))); err != nil {
		t.Errorf("expected remapped path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stageDir, "history.jsonl")); err != nil {
		t.Errorf("non-project file should pass through: %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify the new test fails (and signature breaks compile)**

Run: `go test ./internal/stage/ -v`
Expected: FAIL — compile error (Build takes 3 args) until Step 3.

- [ ] **Step 3: Add the Mapper to Build**

In `internal/stage/stage.go`, change the signature and apply the mapper to the staging destination:

```go
// Mapper rewrites a source-relative path to its staging-relative path. nil means identity
// (mirror the source layout — M1 behavior). M2 supplies a mapper that rewrites
// projects/<encoded-cwd>/<rest> to by-id/<identity>/<rest> so snapshots are machine-independent.
type Mapper func(rel string) string

func Build(srcRoot, stageRoot string, c filter.Classifier, m Mapper) (Result, error) {
	if m == nil {
		m = func(rel string) string { return rel }
	}
	// ... existing setup unchanged ...
```

Then in the WalkDir callback, change the materialize destination from `rel` to the mapped path:

```go
		dstRel := m(rel)
		n, err := materialize(p, filepath.Join(stageRoot, dstRel))
```

(Leave classification, pruning, and Result accounting exactly as they are.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/stage/ -v`
Expected: PASS (existing + new remap test).

- [ ] **Step 5: Commit**

```bash
git add internal/stage/
git commit -m "M2: stage gains a Mapper to key projects/ by identity"
```

---

## Task 5: Resume-aware restore (lay-down)

**Files:**
- Create: `internal/restore/restore.go`
- Test: `internal/restore/restore_test.go`

- [ ] **Step 1: Write the failing test (simulated cross-home)**

```go
package restore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ekinertac/mnemo/internal/manifest"
)

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, c := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A session staged by "machine A" (home -Users-ekinertac) must land at THIS machine's encoded
// home (-Users-ekin) on restore — the core cross-machine guarantee.
func TestLayDownReHomesUnderHomeIdentity(t *testing.T) {
	restored := t.TempDir()
	claude := t.TempDir()
	write(t, restored, map[string]string{
		"by-id/home:-Code-foo/s.jsonl": "session\n",
		"history.jsonl":                "hist\n",
	})
	rep, err := LayDown(restored, claude, "win-desktop", "-Users-ekin", manifest.New())
	if err != nil {
		t.Fatal(err)
	}
	got := filepath.Join(claude, "projects", "-Users-ekin-Code-foo", "s.jsonl")
	if b, err := os.ReadFile(got); err != nil || string(b) != "session\n" {
		t.Errorf("re-homed transcript missing/wrong: %v / %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(claude, "history.jsonl")); err != nil {
		t.Errorf("non-project data should lay straight down: %v", err)
	}
	if rep.LaidDown != 2 {
		t.Errorf("LaidDown = %d, want 2", rep.LaidDown)
	}
}

// An override wins over the home de-tokenization.
func TestLayDownHonorsOverride(t *testing.T) {
	restored := t.TempDir()
	claude := t.TempDir()
	write(t, restored, map[string]string{"by-id/home:-Code-foo/s.jsonl": "x\n"})
	m := manifest.New()
	m.SetOverride("win-desktop", "home:-Code-foo", "/d/work/foo")
	if _, err := LayDown(restored, claude, "win-desktop", "-Users-ekin", m); err != nil {
		t.Fatal(err)
	}
	// "/d/work/foo" encodes to "-d-work-foo".
	if _, err := os.Stat(filepath.Join(claude, "projects", "-d-work-foo", "s.jsonl")); err != nil {
		t.Errorf("override path not used: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/restore/ -v`
Expected: FAIL — `undefined: LayDown`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package restore lays a restored Mnemo staging tree back into ~/.claude for THIS machine. It
// is the inverse of internal/stage's identity keying: by-id/<identity>/<rest> files are
// re-homed to ~/.claude/projects/<local-encoded-cwd>/<rest> so `claude --resume` finds them,
// while non-project data (history.jsonl, transcripts/, plans/, tasks/) lays straight back.
//
// Resolution precedence per identity: manifest override (this host) > home de-tokenization >
// absolute-as-is. Under-home identities always resolve (placement is harmless even if the local
// project dir doesn't exist yet), so M2 never drops a session. Conflict policy is keep-newer at
// file granularity; the .jsonl append-merge is M3 and slots in at writeFile.
//
// Related: internal/identity (the inverse mapping), internal/manifest (overrides),
// internal/command/pull.go (caller), docs/DESIGN.md §5.2.
package restore

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ekinertac/mnemo/internal/identity"
	"github.com/ekinertac/mnemo/internal/manifest"
)

type Report struct {
	LaidDown int
	Unmapped []string // identities with no resolvable local path on this host
}

// LayDown walks restoredRoot and materializes each file into claudeRoot. host/encodedHome and
// the manifest drive identity resolution for by-id/ entries.
func LayDown(restoredRoot, claudeRoot, host, encodedHome string, m *manifest.Manifest) (Report, error) {
	var rep Report
	err := filepath.WalkDir(restoredRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(restoredRoot, p)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)

		dstRel, ok := resolveDst(relSlash, host, encodedHome, m, &rep)
		if !ok {
			return nil // unmapped already recorded
		}
		if err := writeFile(p, filepath.Join(claudeRoot, filepath.FromSlash(dstRel))); err != nil {
			return err
		}
		rep.LaidDown++
		return nil
	})
	return rep, err
}

// resolveDst maps a restored-tree relative path to its ~/.claude relative destination.
func resolveDst(relSlash, host, encodedHome string, m *manifest.Manifest, rep *Report) (string, bool) {
	const byID = "by-id/"
	if !strings.HasPrefix(relSlash, byID) {
		return relSlash, true // non-project data lays straight back
	}
	rest := relSlash[len(byID):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "", false
	}
	id, tail := rest[:slash], rest[slash+1:]

	var localEncoded string
	if ov, ok := m.Override(host, id); ok {
		localEncoded = identity.Encode(stripDrive(ov))
	} else if enc, ok := identity.ToEncoded(identity.Identity(id), encodedHome); ok {
		localEncoded = enc
	} else {
		rep.Unmapped = append(rep.Unmapped, id)
		return "", false
	}
	return "projects/" + localEncoded + "/" + tail, true
}

// stripDrive mirrors identity.EncodedHome's Windows drive handling for override paths.
func stripDrive(p string) string {
	if len(p) >= 2 && p[1] == ':' &&
		((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) {
		return p[2:]
	}
	return p
}

// writeFile copies src to dst (creating parents). M2 policy: last write wins at file level;
// M3 replaces this with append-merge for .jsonl logs.
func writeFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/restore/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/restore/
git commit -m "M2: resume-aware lay-down re-homes identity-keyed sessions"
```

---

## Task 6: Wire identity keying into `mnemo push`

**Files:**
- Modify: `internal/command/push.go`
- Modify: `internal/command/root.go` (add `manifestPath` helper + `hostID`/`nowRFC3339` helpers)

- [ ] **Step 1: Add helpers to root.go**

```go
// hostID is this machine's identity tag for snapshots and the manifest.
func hostID() (string, error) { return os.Hostname() }

// manifestStagePath is where projects.json lives inside the staging tree (repo root).
func manifestStagePath(stageRoot string) string { return filepath.Join(stageRoot, "projects.json") }
```

- [ ] **Step 2: Build the project remap Mapper and write the manifest in push**

In `internal/command/push.go`, after resolving `src` and before `stage.Build`, compute the encoded home and a mapper, and pass it in. Replace the `stage.Build(src, stageRoot, filter.Classifier{})` call:

```go
home, err := os.UserHomeDir()
if err != nil {
	return err
}
encHome := identity.EncodedHome(home)

// Rewrite projects/<encoded-cwd>/<rest> -> by-id/<identity>/<rest>; pass everything else through.
mapper := func(rel string) string {
	relSlash := filepath.ToSlash(rel)
	const pfx = "projects/"
	if !strings.HasPrefix(relSlash, pfx) {
		return rel
	}
	rest := relSlash[len(pfx):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return rel
	}
	enc, tail := rest[:slash], rest[slash+1:]
	id := identity.FromEncoded(enc, encHome)
	return filepath.FromSlash("by-id/" + string(id) + "/" + tail)
}

res, err := stage.Build(src, stageRoot, filter.Classifier{}, mapper)
if err != nil {
	return err
}
```

Then after a successful staging (before the restic backup), load/update/save the manifest into the staging tree so it is captured by the snapshot:

```go
host, err := hostID()
if err != nil {
	return fmt.Errorf("cannot resolve hostname: %w", err)
}
mpath := manifestStagePath(stageRoot)
man, err := manifest.Load(mpath) // empty if first push (we don't yet restore prior manifest — see note)
if err != nil {
	return err
}
man.TouchMachine(host, nowRFC3339())
if err := man.Save(mpath); err != nil {
	return err
}
```

Add imports: `"strings"`, `"github.com/ekinertac/mnemo/internal/identity"`, `"github.com/ekinertac/mnemo/internal/manifest"`. Reuse the existing `host`/`tags` block (remove the duplicate hostname lookup already present so there is exactly one).

> NOTE (carried to Task 8 / a follow-up): a first cut reads the manifest from the freshly-built
> staging tree (empty), so cross-push override history isn't yet merged from the repo. That is
> acceptable for M2's resume path (overrides are host-local and re-applied on pull). Pulling the
> latest manifest from the repo before merge is a small enhancement noted in Task 8.

- [ ] **Step 3: Add nowRFC3339 helper to root.go**

```go
// nowRFC3339 is the one place time enters the command layer; kept here so business logic in
// internal/* stays time-injectable and testable.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }
```
Add `"time"` to root.go imports.

- [ ] **Step 4: Build, vet, run all tests**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: build OK, vet clean, all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/command/push.go internal/command/root.go
git commit -m "M2: push keys projects by identity and stamps the manifest"
```

---

## Task 7: Resume-aware `mnemo pull` + `map`/`projects`/`machines`

**Files:**
- Modify: `internal/command/pull.go`
- Create: `internal/command/map.go`, `internal/command/projects.go`, `internal/command/machines.go`
- Modify: `internal/command/root.go` (register subcommands)

- [ ] **Step 1: Add lay-down to pull (keep --target dry-restore)**

In `internal/command/pull.go`, after the existing `repo.Restore(ctx, *snapFlag, target)` succeeds, when the caller did NOT pass an explicit `--target` (i.e. a real resume), lay down into `~/.claude`. Add a `--lay-down` default-on behavior:

```go
layDown := fs.Bool("lay-down", true, "after restore, lay sessions into ~/.claude for this machine")
// ... after successful Restore into target ...
if *layDown {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	host, err := hostID()
	if err != nil {
		return err
	}
	man, err := manifest.Load(filepath.Join(target, "projects.json"))
	if err != nil {
		return err
	}
	rep, err := restore.LayDown(target, filepath.Join(home, ".claude"), host, identity.EncodedHome(home), man)
	if err != nil {
		return err
	}
	fmt.Printf("mnemo: laid down %d files; %d unmapped\n", rep.LaidDown, len(rep.Unmapped))
	for _, id := range rep.Unmapped {
		fmt.Printf("  unmapped: %s  (use: mnemo map %s <local-path>)\n", id, id)
	}
}
```
Add imports: `"path/filepath"`, identity, manifest, restore.

- [ ] **Step 2: Create `mnemo map`**

```go
// map.go: `mnemo map <identity> <local-path>` records a per-host override in projects.json so a
// project that lives at a non-default path on this machine still resumes correctly. Scriptable,
// non-interactive. The override is written to the staging-tree manifest on the next push; for
// immediate effect it is also applied at pull time from the restored manifest.
package command

import (
	"flag"
	"fmt"

	"github.com/ekinertac/mnemo/internal/manifest"
)

func runMap(args []string) error {
	fs := flag.NewFlagSet("map", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: mnemo map <identity> <local-path>")
	}
	id, path := fs.Arg(0), fs.Arg(1)
	host, err := hostID()
	if err != nil {
		return err
	}
	mpath, err := localManifestPath()
	if err != nil {
		return err
	}
	man, err := manifest.Load(mpath)
	if err != nil {
		return err
	}
	man.SetOverride(host, id, path)
	if err := man.Save(mpath); err != nil {
		return err
	}
	fmt.Printf("mnemo: mapped %s -> %s on %s\n", id, path, host)
	return nil
}
```

Add to root.go a `localManifestPath()` returning `~/.config/mnemo/projects.json` (a local override store consulted by `map`, merged into the repo manifest on push). Create the dir if missing.

- [ ] **Step 3: Create `mnemo machines` and `mnemo projects`**

```go
// machines.go: list devices that have pushed, from the repo manifest (restored to a temp dir).
package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ekinertac/mnemo/internal/manifest"
	"github.com/ekinertac/mnemo/internal/restic"
)

func runMachines(args []string) error {
	fs := flag.NewFlagSet("machines", flag.ContinueOnError)
	repoFlag := fs.String("repo", "", "restic repo location")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	if err := restic.Available(ctx); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "mnemo-machines-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	repo, _ := resolveRepo(*repoFlag)
	if err := repo.Restore(ctx, "latest", tmp); err != nil {
		return err
	}
	man, err := manifest.Load(filepath.Join(tmp, "projects.json"))
	if err != nil {
		return err
	}
	if len(man.Machines) == 0 {
		fmt.Println("mnemo: no machines recorded yet")
		return nil
	}
	for host, mc := range man.Machines {
		fmt.Printf("  %-20s last seen %s\n", host, mc.LastSeen)
	}
	return nil
}
```

`projects.go` follows the same shape: restore latest to a temp dir, walk `by-id/` to list identities, and for each show how it resolves on this host (`identity.ToEncoded` + override check); with `--unmapped`, print only identities whose resolved local `projects/<encoded>` dir does not currently exist under `~/.claude`. (Full code mirrors `machines.go` plus a `--unmapped` bool and the resolution check from `restore.resolveDst`; reuse by exporting a small `restore.ResolveLocal(id, host, encodedHome, *manifest) (string, bool)` and calling it here.)

- [ ] **Step 4: Register subcommands in root.go**

In the `Execute` switch add:
```go
	case "map":
		err = runMap(rest)
	case "projects":
		err = runProjects(rest)
	case "machines":
		err = runMachines(rest)
```
Update `usage()` to list them.

- [ ] **Step 5: Build, vet, test, commit**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all green.
```bash
git add internal/command/
git commit -m "M2: resume-aware pull + map/projects/machines subcommands"
```

---

## Task 8: End-to-end simulated cross-home test + manifest-from-repo merge

**Files:**
- Create: `internal/command/m2_e2e_test.go` (package-level e2e using a local restic repo) OR a shell smoke script under `scripts/`.
- Modify: `internal/command/push.go` (merge prior manifest pulled from repo, per Task 6 note).

- [ ] **Step 1: Write the e2e test (skips if restic absent)**

Drive a real local restic repo: stage a synthetic `~/.claude`-like tree with `projects/-Users-AAA-Code-foo/s.jsonl`, push, then restore to a temp dir and `restore.LayDown` with a *different* encoded home (`-Users-BBB`), asserting the transcript lands at `projects/-Users-BBB-Code-foo/s.jsonl`. Skip with `t.Skip` if `restic.Available` returns an error so CI without restic stays green.

```go
//go:build e2e

package command
// (full harness: set RESTIC_PASSWORD + RESTIC_REPOSITORY to temp dirs, call the same
//  identity/stage/restic/restore functions the commands use, assert re-homed path exists.)
```

- [ ] **Step 2: Run it**

Run: `go test -tags e2e ./internal/command/ -run TestM2CrossHome -v`
Expected: PASS (or SKIP if restic missing).

- [ ] **Step 3: Merge prior manifest from the repo on push**

In `push.go`, before `man.TouchMachine`, restore just `projects.json` from the latest snapshot into a temp dir and load it as the base (so overrides/bookkeeping accumulate across pushes), falling back to empty when there is no snapshot yet. Keep it best-effort (a missing/again-empty repo must not fail the first push).

- [ ] **Step 4: Build, vet, full test**

Run: `go build ./... && go vet ./... && go test ./... && go test -tags e2e ./...`
Expected: all green.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "M2: end-to-end cross-home test; accumulate manifest across pushes"
```

---

## Task 9: Align docs + HANDOFF

**Files:**
- Modify: `docs/DESIGN.md` (§4.4 examples → encoded `home:`/`abs:` form; note step-0 resolved)
- Modify: `docs/superpowers/specs/2026-06-13-m2-project-identity-design.md` (mark step-0 done; representation = encoded tail)
- Modify: `HANDOFF.md` (M2 status, encoding finding)

- [ ] **Step 1: Update DESIGN.md §4.4**

Change the `path:~/Code/foo` examples to the chosen stored form (`home:-Code-foo`, `abs:-opt-services-bar`), and add a one-line note that the readable form is not reconstructable because Claude's encoding (`[^A-Za-z0-9]→-`) is lossy, with the matching contract unchanged.

- [ ] **Step 2: Update the spec + HANDOFF**

In the spec, replace the §3 "pending step 0" note with the resolved decision. In HANDOFF, mark M0–M2 status and record the encoding rule as a hard-won fact.

- [ ] **Step 3: Commit**

```bash
git add docs/ HANDOFF.md
git commit -m "M2: align design docs + HANDOFF with the encoded-identity decision"
```

---

## Self-Review (completed)

- **Spec coverage:** identity resolver (T1–2), projects.json (T3), identity-keyed staging (T4), resume-aware restore (T5), push wiring (T6), pull + map/projects/machines (T7), e2e cross-home + manifest accumulation (T8), docs (T9). All spec §§3–10 covered.
- **Placeholders:** none — every code step shows real, compilable Go. Task 7's `projects.go` and Task 8's e2e harness reference an exported `restore.ResolveLocal` helper; add it when implementing T5 (export `resolveDst`'s identity-resolution core as `ResolveLocal(id, host, encodedHome, *manifest) (encodedDir string, ok bool)` and call it from both `resolveDst` and the projects view) so there is one resolution path, not two.
- **Type consistency:** `Identity`, `Encode`, `EncodedHome`, `FromEncoded`, `ToEncoded`, `Manifest`/`Load`/`SetOverride`/`Override`/`TouchMachine`, `stage.Mapper`/`Build(...,Mapper)`, `restore.LayDown(...)`/`Report` are used consistently across tasks.
- **Refinement vs spec §6:** cross-OS absolute identities are laid down additively (never a holding-area drop) and surfaced by `projects --unmapped`; the holding concept reduces to malformed-identity safety. Captured in DESIGN/spec alignment (T9).
