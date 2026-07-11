package restore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ekinertac/mnemo/internal/manifest"
	"github.com/ekinertac/mnemo/internal/merge"
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
		"by-id/home_-Code-foo/s.jsonl": "session\n", // path-safe identity (':' -> '_'), as push writes it
		"history.jsonl":                "hist\n",
	})
	rep, err := LayDown(restored, claude, "win-desktop", "-Users-ekin", manifest.New(), nil)
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
	write(t, restored, map[string]string{"by-id/home_-Code-foo/s.jsonl": "x\n"})
	m := manifest.New()
	m.SetOverride("win-desktop", "home:-Code-foo", "/d/work/foo")
	if _, err := LayDown(restored, claude, "win-desktop", "-Users-ekin", m, nil); err != nil {
		t.Fatal(err)
	}
	// "/d/work/foo" encodes to "-d-work-foo".
	if _, err := os.Stat(filepath.Join(claude, "projects", "-d-work-foo", "s.jsonl")); err != nil {
		t.Errorf("override path not used: %v", err)
	}
}

// ResolveLocal precedence: override > home > abs; malformed -> ok=false.
func TestResolveLocal(t *testing.T) {
	m := manifest.New()
	if enc, ok := ResolveLocal("home:-Code-foo", "h", "-Users-ekin", m); !ok || enc != "-Users-ekin-Code-foo" {
		t.Errorf("home resolve = %q,%v", enc, ok)
	}
	if enc, ok := ResolveLocal("abs:-opt-bar", "h", "-Users-ekin", m); !ok || enc != "-opt-bar" {
		t.Errorf("abs resolve = %q,%v", enc, ok)
	}
	m.SetOverride("h", "home:-Code-foo", "/d/work/foo")
	if enc, ok := ResolveLocal("home:-Code-foo", "h", "-Users-ekin", m); !ok || enc != "-d-work-foo" {
		t.Errorf("override resolve = %q,%v", enc, ok)
	}
	if _, ok := ResolveLocal("garbage", "h", "-Users-ekin", m); ok {
		t.Error("malformed identity should be ok=false")
	}
}

// Non-project subtrees beyond history.jsonl (transcripts/, plans/, tasks/) must also pass
// straight through — the spec calls these out explicitly.
func TestLayDownPassesThroughNonProjectData(t *testing.T) {
	restored := t.TempDir()
	claude := t.TempDir()
	write(t, restored, map[string]string{
		"transcripts/ses_x.jsonl": "t\n",
		"plans/p.md":              "p\n",
		"tasks/t.json":            "{}\n",
	})
	rep, err := LayDown(restored, claude, "h", "-Users-ekin", manifest.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"transcripts/ses_x.jsonl", "plans/p.md", "tasks/t.json"} {
		if _, err := os.Stat(filepath.Join(claude, filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected pass-through %q: %v", rel, err)
		}
	}
	if rep.LaidDown != 3 {
		t.Errorf("LaidDown = %d, want 3", rep.LaidDown)
	}
}

// An existing .jsonl at the destination must be UNION-merged with the incoming one (M3), not
// clobbered — the fix for claude-sync's last-writer-wins data loss.
func TestLayDownMergesExistingJSONL(t *testing.T) {
	restored := t.TempDir()
	claude := t.TempDir()
	const a = `{"timestamp":"2026-01-01T00:00:01Z","v":"A"}`
	const b = `{"timestamp":"2026-01-01T00:00:02Z","v":"B"}`
	const c = `{"timestamp":"2026-01-01T00:00:03Z","v":"C"}`
	// Local already has A, C; the incoming snapshot has A, B. Neither side may lose a line.
	write(t, claude, map[string]string{"history.jsonl": a + "\n" + c + "\n"})
	write(t, restored, map[string]string{"history.jsonl": a + "\n" + b + "\n"})

	if _, err := LayDown(restored, claude, "h", "-Users-ekin", manifest.New(), nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(claude, "history.jsonl"))
	want := a + "\n" + b + "\n" + c + "\n" // union, chronological
	if string(got) != want {
		t.Errorf("merged history =\n%s\nwant\n%s", got, want)
	}
}

// A non-.jsonl existing file is newer-mtime-wins (only append-only logs merge; .md 3-way-merges).
func TestLayDownOverwritesNonJSONL(t *testing.T) {
	restored := t.TempDir()
	claude := t.TempDir()
	write(t, claude, map[string]string{"by-id/home_-Code-foo/memory/note.md": "old\n"})
	write(t, restored, map[string]string{"by-id/home_-Code-foo/memory/note.md": "new\n"})
	// existing local dir where LayDown will write:
	dst := filepath.Join(claude, "projects", "-Users-ekin-Code-foo", "memory", "note.md")
	os.MkdirAll(filepath.Dir(dst), 0o755)
	os.WriteFile(dst, []byte("old\n"), 0o644)
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	os.Chtimes(dst, older, older)
	os.Chtimes(filepath.Join(restored, "by-id", "home_-Code-foo", "memory", "note.md"), newer, newer)
	if _, err := LayDown(restored, claude, "h", "-Users-ekin", manifest.New(), nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "new\n" {
		t.Errorf("non-jsonl newer-wins = %q, want \"new\\n\"", got)
	}
}

// claude --resume sorts sessions by file mtime, but the atomic write stamps every laid-down file
// with "now". LayDown must reset a transcript's mtime to the newest timestamp INSIDE it so the
// restored sessions keep their true last-activity order. The newest timestamp may sit mid-file
// (summary/custom-title events interleave), so it is not simply the last line.
func TestLayDownSetsMtimeFromNewestTimestamp(t *testing.T) {
	restored := t.TempDir()
	claude := t.TempDir()
	lines := `{"timestamp":"2026-03-01T10:00:00Z"}` + "\n" +
		`{"timestamp":"2026-06-15T09:30:00Z"}` + "\n" + // newest, and NOT the last line
		`{"type":"summary"}` + "\n" // a line without a timestamp must be tolerated
	write(t, restored, map[string]string{"by-id/home_-Code-foo/s.jsonl": lines})
	if _, err := LayDown(restored, claude, "h", "-Users-ekin", manifest.New(), nil); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(claude, "projects", "-Users-ekin-Code-foo", "s.jsonl")
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := time.Parse(time.RFC3339, "2026-06-15T09:30:00Z")
	if !fi.ModTime().Equal(want) {
		t.Errorf("mtime = %s, want %s (newest internal timestamp)", fi.ModTime().UTC(), want)
	}
}

// history.jsonl encodes timestamps as integer ms-epoch (not ISO strings); its mtime must still be
// restored from that integer form.
func TestLayDownSetsMtimeFromIntegerTimestamp(t *testing.T) {
	restored := t.TempDir()
	claude := t.TempDir()
	write(t, restored, map[string]string{"history.jsonl": `{"timestamp":1700000000000}` + "\n"})
	if _, err := LayDown(restored, claude, "h", "-Users-ekin", manifest.New(), nil); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(claude, "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.UnixMilli(1700000000000); !fi.ModTime().Equal(want) {
		t.Errorf("mtime = %s, want %s", fi.ModTime(), want)
	}
}

// An unchanged .md (identical on both sides) must NOT trigger the base fetch: the base is a remote
// restic dump, and doing it per-file for hundreds of untouched memory files is the sync's main cost.
func TestLayDownSkipsBaseFetchForUnchangedMarkdown(t *testing.T) {
	restored := t.TempDir()
	claude := t.TempDir()
	const same = "one\ntwo\nthree\n"
	write(t, claude, map[string]string{"projects/-Users-ekin-Code-foo/memory/n.md": same})
	write(t, restored, map[string]string{"by-id/home_-Code-foo/memory/n.md": same})
	called := false
	baseFn := func(string) ([]byte, bool) { called = true; return []byte("base"), true }
	rep, err := LayDown(restored, claude, "h", "-Users-ekin", manifest.New(), baseFn)
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("base fetch ran for an unchanged .md; it should be skipped")
	}
	if len(rep.Conflicted) != 0 {
		t.Errorf("Conflicted = %v, want empty", rep.Conflicted)
	}
	got, _ := os.ReadFile(filepath.Join(claude, "projects", "-Users-ekin-Code-foo", "memory", "n.md"))
	if string(got) != same {
		t.Errorf("unchanged .md was altered: %q", got)
	}
}

// A corrupt project identity (one carrying an unexpanded ${HOME}, which Claude's encoding can never
// produce) must be skipped, not laid down, and surfaced in the report. Valid siblings still land.
func TestLayDownSkipsCorruptIdentity(t *testing.T) {
	restored := t.TempDir()
	claude := t.TempDir()
	write(t, restored, map[string]string{
		"by-id/abs_${HOME}-Code-foo/memory/x.md": "junk\n", // ':' -> '_' path-safe; ${HOME} is corrupt
		"by-id/home_-Code-ok/s.jsonl":            "good\n",
	})
	rep, err := LayDown(restored, claude, "h", "-Users-ekin", manifest.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(claude, "projects", "-Users-ekin-Code-ok", "s.jsonl")); err != nil {
		t.Errorf("valid session should still lay down: %v", err)
	}
	if _, err := os.Stat(filepath.Join(claude, "projects", "${HOME}-Code-foo", "memory", "x.md")); !os.IsNotExist(err) {
		t.Error("corrupt ${HOME} identity was laid down; it should be skipped")
	}
	if len(rep.Skipped) != 1 {
		t.Errorf("Skipped = %v, want exactly one corrupt identity", rep.Skipped)
	}
}

// A file directly under by-id/ (no identity subdir) must be surfaced in Unmapped, never
// silently dropped — the never-drop invariant.
func TestLayDownSurfacesMalformedByID(t *testing.T) {
	restored := t.TempDir()
	claude := t.TempDir()
	write(t, restored, map[string]string{"by-id/orphan": "x\n"})
	rep, err := LayDown(restored, claude, "h", "-Users-ekin", manifest.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.LaidDown != 0 || len(rep.Unmapped) != 1 {
		t.Errorf("orphan by-id file: LaidDown=%d Unmapped=%v, want 0 and one entry", rep.LaidDown, rep.Unmapped)
	}
}

// A .md present on both sides 3-way-merges: non-overlapping edits from both machines survive, no
// markers, nothing reported as conflicted.
func TestLayDownMergesMarkdownBothEdits(t *testing.T) {
	if err := merge.GitAvailable(); err != nil {
		t.Skip("git not available:", err)
	}
	restored := t.TempDir()
	claude := t.TempDir()
	base := "one\ntwo\nthree\n"
	// local (ours) changed line 1; incoming (theirs) changed line 3.
	write(t, claude, map[string]string{"projects/-Users-ekin-Code-foo/memory/n.md": "one LOCAL\ntwo\nthree\n"})
	write(t, restored, map[string]string{"by-id/home_-Code-foo/memory/n.md": "one\ntwo\nthree REMOTE\n"})

	baseFn := func(rel string) ([]byte, bool) { return []byte(base), true }
	rep, err := LayDown(restored, claude, "h", "-Users-ekin", manifest.New(), baseFn)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(claude, "projects", "-Users-ekin-Code-foo", "memory", "n.md"))
	if !strings.Contains(string(got), "one LOCAL") || !strings.Contains(string(got), "three REMOTE") {
		t.Fatalf("merge lost an edit:\n%s", got)
	}
	if len(rep.Conflicted) != 0 {
		t.Fatalf("Conflicted = %v, want empty", rep.Conflicted)
	}
}

// Same-line collision -> markers kept AND the file is reported in Conflicted.
func TestLayDownReportsMarkdownConflict(t *testing.T) {
	if err := merge.GitAvailable(); err != nil {
		t.Skip("git not available:", err)
	}
	restored := t.TempDir()
	claude := t.TempDir()
	write(t, claude, map[string]string{"projects/-Users-ekin-Code-foo/memory/n.md": "LOCAL\n"})
	write(t, restored, map[string]string{"by-id/home_-Code-foo/memory/n.md": "REMOTE\n"})
	baseFn := func(rel string) ([]byte, bool) { return []byte("BASE\n"), true }
	rep, err := LayDown(restored, claude, "h", "-Users-ekin", manifest.New(), baseFn)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(claude, "projects", "-Users-ekin-Code-foo", "memory", "n.md"))
	if !strings.Contains(string(got), "<<<<<<<") {
		t.Fatalf("expected conflict markers:\n%s", got)
	}
	want := "projects/-Users-ekin-Code-foo/memory/n.md"
	if len(rep.Conflicted) != 1 || rep.Conflicted[0] != want {
		t.Fatalf("Conflicted = %v, want [%s]", rep.Conflicted, want)
	}
}

// With no recoverable base, a .md falls back to newer-mtime-wins.
func TestLayDownMarkdownNoBaseNewerWins(t *testing.T) {
	restored := t.TempDir()
	claude := t.TempDir()
	write(t, claude, map[string]string{"projects/-Users-ekin-Code-foo/memory/n.md": "LOCAL\n"})
	write(t, restored, map[string]string{"by-id/home_-Code-foo/memory/n.md": "REMOTE\n"})
	// Make the incoming file the newer one deterministically.
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	os.Chtimes(filepath.Join(claude, "projects", "-Users-ekin-Code-foo", "memory", "n.md"), older, older)
	os.Chtimes(filepath.Join(restored, "by-id", "home_-Code-foo", "memory", "n.md"), newer, newer)

	noBase := func(rel string) ([]byte, bool) { return nil, false }
	if _, err := LayDown(restored, claude, "h", "-Users-ekin", manifest.New(), noBase); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(claude, "projects", "-Users-ekin-Code-foo", "memory", "n.md"))
	if string(got) != "REMOTE\n" {
		t.Fatalf("newer-wins: got %q, want REMOTE (incoming was newer)", got)
	}
}

// Local newer than incoming -> local kept.
func TestLayDownNewerWinsKeepsLocal(t *testing.T) {
	restored := t.TempDir()
	claude := t.TempDir()
	write(t, claude, map[string]string{"projects/-Users-ekin-Code-foo/memory/n.md": "LOCAL\n"})
	write(t, restored, map[string]string{"by-id/home_-Code-foo/memory/n.md": "REMOTE\n"})
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	os.Chtimes(filepath.Join(claude, "projects", "-Users-ekin-Code-foo", "memory", "n.md"), newer, newer)
	os.Chtimes(filepath.Join(restored, "by-id", "home_-Code-foo", "memory", "n.md"), older, older)
	noBase := func(rel string) ([]byte, bool) { return nil, false }
	if _, err := LayDown(restored, claude, "h", "-Users-ekin", manifest.New(), noBase); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(claude, "projects", "-Users-ekin-Code-foo", "memory", "n.md"))
	if string(got) != "LOCAL\n" {
		t.Fatalf("newer-wins: got %q, want LOCAL (local was newer)", got)
	}
}
