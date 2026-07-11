package stage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ekinertac/mnemo/internal/filter"
)

// A mapper returning "" means "skip this file"; Build must not materialize it and must count it
// as corrupt (used to drop project dirs with a broken, unencodable identity).
func TestBuildSkipsWhenMapperReturnsEmpty(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeTree(t, src, map[string]string{
		"history.jsonl":                      "keep", // durable, passes through
		"projects/-Users-x-Code-foo/s.jsonl": "drop", // durable, but the mapper drops it
	})
	drop := func(rel string) string {
		if filepath.Base(rel) == "s.jsonl" {
			return ""
		}
		return rel
	}
	res, err := Build(src, dst, filter.Classifier{}, drop)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "projects", "-Users-x-Code-foo", "s.jsonl")); !os.IsNotExist(err) {
		t.Error("file the mapper dropped was still materialized")
	}
	if _, err := os.Stat(filepath.Join(dst, "history.jsonl")); err != nil {
		t.Errorf("kept file missing: %v", err)
	}
	if res.Corrupt != 1 {
		t.Errorf("Corrupt = %d, want 1", res.Corrupt)
	}
	if res.Included != 1 {
		t.Errorf("Included = %d, want 1", res.Included)
	}
}

// writeTree materializes a map of relpath->content under root, creating parent dirs.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Build must produce a staging tree containing exactly the Durable files, byte-identical,
// at the same relative layout — and nothing classified Ephemeral/Config/Unknown.
func TestBuildSelectsOnlyDurable(t *testing.T) {
	src := t.TempDir()
	stageDir := t.TempDir()

	writeTree(t, src, map[string]string{
		// durable
		"history.jsonl": "h\n",
		"projects/-Users-x-Code-foo/session-1.jsonl": "s1\n",
		"projects/-Users-x-Code-foo/memory/note.md":  "mem\n",
		"plans/p.md":     "plan\n",
		"tasks/t-1.json": "{}\n",
		// skipped: ephemeral
		"projects/-Users-x-Code-foo/subagents/a.jsonl":  "scratch\n",
		"projects/-Users-x-Code-foo/tool-results/o.txt": "dump\n",
		"tasks/t-1/.lock": "lock\n",
		// skipped: config (and a deep file that must not be walked into)
		"settings.json":                 "cfg\n",
		"plugins/big/node_modules/x.js": "junk\n",
		"skills/s/SKILL.md":             "skill\n",
		// skipped: unknown
		"cache/blob": "cache\n",
	})

	res, err := Build(src, stageDir, filter.Classifier{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	wantPresent := []string{
		"history.jsonl",
		"projects/-Users-x-Code-foo/session-1.jsonl",
		"projects/-Users-x-Code-foo/memory/note.md",
		"plans/p.md",
		"tasks/t-1.json",
	}
	wantAbsent := []string{
		"projects/-Users-x-Code-foo/subagents/a.jsonl",
		"projects/-Users-x-Code-foo/tool-results/o.txt",
		"tasks/t-1/.lock",
		"settings.json",
		"plugins/big/node_modules/x.js",
		"skills/s/SKILL.md",
		"cache/blob",
	}

	for _, rel := range wantPresent {
		got, err := os.ReadFile(filepath.Join(stageDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("expected staged %q: %v", rel, err)
			continue
		}
		orig, _ := os.ReadFile(filepath.Join(src, filepath.FromSlash(rel)))
		if string(got) != string(orig) {
			t.Errorf("staged %q content = %q, want %q", rel, got, orig)
		}
	}
	for _, rel := range wantAbsent {
		if _, err := os.Stat(filepath.Join(stageDir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("did not expect staged %q (err=%v)", rel, err)
		}
	}

	if res.Included != len(wantPresent) {
		t.Errorf("Result.Included = %d, want %d", res.Included, len(wantPresent))
	}
	if res.Skipped[filter.Config] < 3 {
		t.Errorf("Result.Skipped[Config] = %d, want >=3", res.Skipped[filter.Config])
	}
	if res.Skipped[filter.Ephemeral] < 3 {
		t.Errorf("Result.Skipped[Ephemeral] = %d, want >=3", res.Skipped[filter.Ephemeral])
	}
}

// A missing source root is an error the caller can report, not a panic.
func TestBuildMissingSource(t *testing.T) {
	if _, err := Build(filepath.Join(t.TempDir(), "nope"), t.TempDir(), filter.Classifier{}, nil); err == nil {
		t.Error("expected error for missing source root, got nil")
	}
}

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
	res, err := Build(src, stageDir, filter.Classifier{}, mapper)
	if err != nil {
		t.Fatal(err)
	}
	if res.Included != 2 {
		t.Errorf("Result.Included = %d, want 2", res.Included)
	}
	if _, err := os.Stat(filepath.Join(stageDir, filepath.FromSlash("by-id/home:-Code-foo/s.jsonl"))); err != nil {
		t.Errorf("expected remapped path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stageDir, "history.jsonl")); err != nil {
		t.Errorf("non-project file should pass through: %v", err)
	}
}

func TestCopyFilePreservesMtime(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	if err := os.Chtimes(src, want, want); err != nil {
		t.Fatal(err)
	}
	if _, err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.ModTime().Equal(want) {
		t.Fatalf("dst mtime = %s, want %s", fi.ModTime().UTC(), want)
	}
}
