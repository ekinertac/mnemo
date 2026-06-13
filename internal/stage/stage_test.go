package stage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ekinertac/mnemo/internal/filter"
)

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
		"history.jsonl":                                  "h\n",
		"projects/-Users-x-Code-foo/session-1.jsonl":     "s1\n",
		"projects/-Users-x-Code-foo/memory/note.md":      "mem\n",
		"plans/p.md":                                     "plan\n",
		"tasks/t-1.json":                                 "{}\n",
		// skipped: ephemeral
		"projects/-Users-x-Code-foo/subagents/a.jsonl":   "scratch\n",
		"projects/-Users-x-Code-foo/tool-results/o.txt":  "dump\n",
		"tasks/t-1/.lock":                                "lock\n",
		// skipped: config (and a deep file that must not be walked into)
		"settings.json":                                  "cfg\n",
		"plugins/big/node_modules/x.js":                  "junk\n",
		"skills/s/SKILL.md":                              "skill\n",
		// skipped: unknown
		"cache/blob":                                     "cache\n",
	})

	res, err := Build(src, stageDir, filter.Classifier{})
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
	if _, err := Build(filepath.Join(t.TempDir(), "nope"), t.TempDir(), filter.Classifier{}); err == nil {
		t.Error("expected error for missing source root, got nil")
	}
}
