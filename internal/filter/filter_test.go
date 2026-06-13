package filter

import "testing"

// These tests pin DESIGN §4.1's three-category split into executable rules. The filter is
// an *allowlist*: only explicitly-recognized session data is Durable; everything else
// defaults to skipped (Unknown) so the sessions-only boundary can't erode by accident.
func TestClassify(t *testing.T) {
	cases := []struct {
		rel  string
		want Class
	}{
		// (A) Durable — the session data we exist to sync.
		{"history.jsonl", Durable},
		{"projects/-Users-ekinertac-Code-foo/session-abc.jsonl", Durable},
		{"projects/-Users-ekinertac-Code-foo/memory/note.md", Durable},
		{"projects/-Users-ekinertac-Code-foo/memory/sub/deep.md", Durable},
		{"plans/plan-1.md", Durable},
		{"plans/nested/plan-2.md", Durable},
		{"tasks/task-123.json", Durable},
		// Legacy bridge-surface transcripts (top-level transcripts/, lighter schema). Real
		// conversation data not mirrored in projects/, so durable. Only .jsonl qualifies.
		{"transcripts/ses_44b0a2c7cffezRXJ4RqOeNIv62.jsonl", Durable},

		// (B) Ephemeral — regenerable scratch, skipped even though some end in .jsonl.
		{"projects/-Users-x/subagents/sub.jsonl", Ephemeral},
		{"projects/-Users-x/workflows/wf.json", Ephemeral},
		{"projects/-Users-x/some/nested/workflows/wf.json", Ephemeral},
		{"projects/-Users-x/tool-results/out.txt", Ephemeral},
		{"tasks/task-123/.lock", Ephemeral},
		{"tasks/task-123/.highwatermark", Ephemeral},

		// (C) Config / capabilities — the hard boundary, never synced.
		{"settings.json", Config},
		{"settings.local.json", Config},
		{"CLAUDE.md", Config},
		{"mcp.json", Config},
		{"skills/foo/SKILL.md", Config},
		{"agents/bar.md", Config},
		{"plugins/x/node_modules/y.js", Config},
		{"rules/z.md", Config},

		// Unknown — not recognized as session data; skipped by the allowlist default.
		{"cache/blob", Unknown},
		{"backups/old", Unknown},
		{"history.jsonl.conflict.20260613-200537", Unknown},
		{"projects/-Users-x/odd-artifact.bin", Unknown},
		{"tasks/task-123/other.txt", Unknown},
		{"transcripts/index.json", Unknown}, // only *.jsonl under transcripts/ is durable
		{"sessions/28854.json", Unknown},    // live runtime pointers (pid/status), not data
	}

	var c Classifier
	for _, tc := range cases {
		if got := c.Classify(tc.rel); got != tc.want {
			t.Errorf("Classify(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
}

// Windows-style separators must classify identically — project-identity work (M2) leans on
// this, but even at M1 a path must not be mis-skipped because of a backslash.
func TestClassifyNormalizesSeparators(t *testing.T) {
	var c Classifier
	if got := c.Classify(`projects\-Users-x\session.jsonl`); got != Durable {
		t.Errorf("backslash path = %v, want Durable", got)
	}
	if got := c.Classify(`projects\-Users-x\subagents\s.jsonl`); got != Ephemeral {
		t.Errorf("backslash ephemeral = %v, want Ephemeral", got)
	}
}

// A user exclude glob downgrades an otherwise-Durable path to skipped (Ephemeral) — DESIGN
// §4.1(B) lists user excludes under "skipped". Config stays a hard boundary regardless.
func TestClassifyUserExcludes(t *testing.T) {
	c := Classifier{Exclude: []string{"projects/*/memory/secret-*"}}
	if got := c.Classify("projects/-Users-x/memory/secret-key.md"); got != Ephemeral {
		t.Errorf("user-excluded path = %v, want Ephemeral", got)
	}
	// A sibling that does NOT match the glob stays Durable.
	if got := c.Classify("projects/-Users-x/memory/normal.md"); got != Durable {
		t.Errorf("non-excluded sibling = %v, want Durable", got)
	}
}

func TestClassInclude(t *testing.T) {
	if !Durable.Include() {
		t.Error("Durable.Include() = false, want true")
	}
	for _, c := range []Class{Unknown, Ephemeral, Config} {
		if c.Include() {
			t.Errorf("%v.Include() = true, want false", c)
		}
	}
}
