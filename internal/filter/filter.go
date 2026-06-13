// Package filter is Mnemo's ephemeral filter (DESIGN §5.1): pure classification of paths
// under ~/.claude into "what to back up" and "what to skip". It is the gatekeeper that
// enforces the sessions-only boundary (principle 9) before anything reaches the staging
// tree, so it deliberately knows nothing about restic, snapshots, or disk layout — given a
// relative path string it returns a Class, nothing more. That purity is why it's the
// TDD-anchored core of M1.
//
// The model is an ALLOWLIST, not a denylist: only paths we positively recognize as session
// data are Durable; everything unrecognized defaults to Unknown (skipped). This is the
// structural reason Mnemo can't accidentally start syncing config or scratch — a new file
// type Claude Code invents lands in Unknown and is ignored until we explicitly include it.
//
// The three skip-vs-keep categories come straight from DESIGN §4.1:
//   - Durable   (A): session transcripts, per-project memory, plans, task state, history.
//   - Ephemeral (B): subagent/workflow/tool-result scratch, runner locks, user excludes.
//   - Config    (C): settings, MCP, skills, agents, plugins, rules, CLAUDE.md — hard boundary.
//
// Callers (the staging-tree builder in internal/stage) walk ~/.claude and keep only paths
// where Class.Include() is true. Related reading: docs/DESIGN.md §4.1 and §5.1.
package filter

import (
	"path"
	"strings"
)

// Class is the backup disposition of a single path relative to the ~/.claude root.
type Class int

const (
	// Unknown is the allowlist default: not recognized as session data, so skipped. Keeping
	// this as the zero value means "forgot to classify" fails safe (skip), never leaks.
	Unknown Class = iota
	// Durable is session data we sync — the whole point of Mnemo.
	Durable
	// Ephemeral is regenerable scratch (and user-excluded paths); skipped.
	Ephemeral
	// Config is capabilities/configuration; never synced — a hard boundary, not a toggle.
	Config
)

func (c Class) String() string {
	switch c {
	case Durable:
		return "durable"
	case Ephemeral:
		return "ephemeral"
	case Config:
		return "config"
	default:
		return "unknown"
	}
}

// Include reports whether a path of this class belongs in the backup. Only Durable does.
func (c Class) Include() bool { return c == Durable }

// Classifier classifies paths. The zero value applies only Mnemo's built-in rules; set
// Exclude to add user globs that downgrade otherwise-Durable paths to skipped (Ephemeral).
type Classifier struct {
	// Exclude holds user-supplied globs (path.Match syntax, "/"-separated). A path matching
	// any of them is treated as Ephemeral even if a built-in rule would keep it. Config is
	// never affected — the hard boundary wins over user intent in the keep direction only.
	Exclude []string
}

// Classify returns the Class of rel, a path relative to ~/.claude. Separators are normalized
// so Windows-style backslashes classify identically to POSIX (M2's identity work depends on
// this, but even here a backslash must not cause a mis-skip).
//
// Evaluation order matters and encodes the precedence rules:
//  1. Config (hard boundary) — checked first; nothing downgrades it.
//  2. User excludes — let the user skip otherwise-durable data.
//  3. Built-in Ephemeral — scratch inside projects/ and runner files, before the
//     Durable rules, because e.g. subagent scratch also ends in .jsonl.
//  4. Durable — the positive allowlist.
//  5. Unknown — anything unmatched.
func (c Classifier) Classify(rel string) Class {
	rel = strings.ReplaceAll(rel, `\`, "/")
	rel = strings.TrimPrefix(rel, "./")
	segs := strings.Split(rel, "/")
	top := segs[0]

	// (1) Config / capabilities — never synced.
	switch top {
	case "settings.json", "settings.local.json", "mcp.json", "CLAUDE.md",
		"skills", "agents", "plugins", "rules":
		return Config
	}

	// (2) User excludes downgrade to skipped.
	for _, g := range c.Exclude {
		if ok, _ := path.Match(g, rel); ok {
			return Ephemeral
		}
	}

	switch top {
	case "projects":
		return classifyProject(segs)
	case "tasks":
		return classifyTask(segs)
	case "plans":
		// All plan-mode work product is durable.
		return Durable
	case "transcripts":
		// Legacy bridge-surface transcript store (DESIGN §4.1 predates it): top-level
		// transcripts/<id>.jsonl are real conversations from the desktop/web bridge, not
		// mirrored in projects/. Treat the .jsonl as durable; any index/sidecar is Unknown.
		if len(segs) == 2 && strings.HasSuffix(segs[1], ".jsonl") {
			return Durable
		}
	case "history.jsonl":
		// Only the exact log is durable; .conflict.* sidecars fall through to Unknown until
		// M3's merge logic handles them.
		if len(segs) == 1 {
			return Durable
		}
	}
	return Unknown
}

// classifyProject handles paths under projects/<id>/. Scratch subtrees are Ephemeral; the
// session transcript (*.jsonl directly under the project) and memory/ are Durable; anything
// else is Unknown so we don't sweep up stray artifacts.
func classifyProject(segs []string) Class {
	// segs = ["projects", "<id>", rest...]
	if len(segs) < 3 {
		return Unknown // projects/ itself or a bare project dir — nothing to classify.
	}
	rest := segs[2:]
	for _, s := range rest {
		switch s {
		case "subagents", "workflows", "tool-results":
			return Ephemeral
		}
	}
	if rest[0] == "memory" {
		return Durable
	}
	// A transcript lives directly in the project dir: projects/<id>/<session>.jsonl.
	if len(rest) == 1 && strings.HasSuffix(rest[0], ".jsonl") {
		return Durable
	}
	return Unknown
}

// classifyTask handles paths under tasks/. tasks/<id>.json is durable state; tasks/<id>/.lock
// and .highwatermark are runner scratch; deeper task files are Unknown for now.
func classifyTask(segs []string) Class {
	// segs = ["tasks", rest...]
	if len(segs) == 2 && strings.HasSuffix(segs[1], ".json") {
		return Durable
	}
	if len(segs) >= 3 {
		switch segs[len(segs)-1] {
		case ".lock", ".highwatermark":
			return Ephemeral
		}
	}
	return Unknown
}
