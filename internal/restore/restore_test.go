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
		"by-id/home_-Code-foo/s.jsonl": "session\n", // path-safe identity (':' -> '_'), as push writes it
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
	write(t, restored, map[string]string{"by-id/home_-Code-foo/s.jsonl": "x\n"})
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
	rep, err := LayDown(restored, claude, "h", "-Users-ekin", manifest.New())
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

// A file directly under by-id/ (no identity subdir) must be surfaced in Unmapped, never
// silently dropped — the never-drop invariant.
func TestLayDownSurfacesMalformedByID(t *testing.T) {
	restored := t.TempDir()
	claude := t.TempDir()
	write(t, restored, map[string]string{"by-id/orphan": "x\n"})
	rep, err := LayDown(restored, claude, "h", "-Users-ekin", manifest.New())
	if err != nil {
		t.Fatal(err)
	}
	if rep.LaidDown != 0 || len(rep.Unmapped) != 1 {
		t.Errorf("orphan by-id file: LaidDown=%d Unmapped=%v, want 0 and one entry", rep.LaidDown, rep.Unmapped)
	}
}
