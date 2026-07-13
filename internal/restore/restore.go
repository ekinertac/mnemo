// Package restore lays a restored Mnemo staging tree back into ~/.claude for THIS machine. It
// is the inverse of internal/stage's identity keying: by-id/<identity>/<rest> files are
// re-homed to ~/.claude/projects/<local-encoded-cwd>/<rest> so `claude --resume` finds them,
// while non-project data (history.jsonl, transcripts/, plans/, tasks/) lays straight back.
//
// Resolution precedence per identity: manifest override (this host) > home de-tokenization >
// absolute-as-is. Under-home identities always resolve (placement is harmless even if the local
// project dir doesn't exist yet), so M2 never drops a session. Conflict policy is last-write-wins
// at file granularity; the .jsonl append-merge is M3 and slots in at writeFile.
//
// Related: internal/identity (the inverse mapping), internal/manifest (overrides),
// internal/command/pull.go (caller), docs/DESIGN.md §5.2.
package restore

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ekinertac/mnemo/internal/identity"
	"github.com/ekinertac/mnemo/internal/manifest"
	"github.com/ekinertac/mnemo/internal/merge"
)

type Report struct {
	LaidDown int
	Unmapped []string // identities with no resolvable local path on this host
	Skipped  []string // corrupt identities refused (e.g. an unexpanded ${HOME}); never laid down
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
		dst := filepath.Join(claudeRoot, filepath.FromSlash(dstRel))
		if err := writeFile(p, dst); err != nil {
			return err
		}
		// Restore the session's real last-activity mtime; the atomic write above stamped it "now".
		if strings.HasSuffix(dst, ".jsonl") {
			stampMtimeFromContent(dst)
		}
		rep.LaidDown++
		return nil
	})
	return rep, err
}

// resolveDst maps a restored-tree relative path (forward-slash) to its ~/.claude relative
// destination (forward-slash). Non-project data passes through; by-id/<id>/<rest> is re-homed.
func resolveDst(relSlash, host, encodedHome string, m *manifest.Manifest, rep *Report) (string, bool) {
	const byID = "by-id/"
	if !strings.HasPrefix(relSlash, byID) {
		return relSlash, true // non-project data lays straight back
	}
	rest := relSlash[len(byID):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		// A file directly under by-id/ has no identity subdir — anomalous (staging never emits
		// these). Surface it in Unmapped rather than silently dropping it; we won't guess a
		// destination, but the never-drop invariant means it must not vanish unreported.
		rep.Unmapped = append(rep.Unmapped, rest)
		return "", false
	}
	// The dir component is the path-safe identity (':' -> '_'); recover the canonical identity
	// before resolving so manifest-override lookups (keyed by the canonical form) match.
	idSeg, tail := rest[:slash], rest[slash+1:]
	id := identity.FromPathSafe(idSeg)
	localEncoded, ok := ResolveLocal(id, host, encodedHome, m)
	if !ok {
		rep.Unmapped = append(rep.Unmapped, string(id))
		return "", false
	}
	if !identity.Valid(localEncoded) {
		// A resolved dir name that still isn't pure [A-Za-z0-9-] came from a corrupt identity —
		// typically an unexpanded ${HOME} baked into an old snapshot by a broken restore. Refuse it:
		// laying it down recreates garbage project dirs Claude can never read, and the next push
		// re-propagates them, so a single poisoned entry otherwise loops forever.
		rep.Skipped = append(rep.Skipped, string(id))
		return "", false
	}
	return "projects/" + localEncoded + "/" + tail, true
}

// ResolveLocal maps an identity to THIS machine's encoded local cwd dir name. Precedence:
// per-host override > home de-tokenization > absolute-as-is. ok=false only for a malformed
// identity (no known scheme). Exported so the `mnemo projects` view resolves identities the
// exact same way restore does (one resolution path, not two).
func ResolveLocal(id identity.Identity, host, encodedHome string, m *manifest.Manifest) (string, bool) {
	if ov, ok := m.Override(host, string(id)); ok {
		// Encode the override path the same way EncodedHome does (drive-strip then Claude-encode),
		// so an override resolves to a dir name Claude would actually use.
		return identity.Encode(identity.StripWindowsDrive(ov)), true
	}
	return identity.ToEncoded(id, encodedHome)
}

// writeFile lays the incoming file (src) down at dst, creating parents. Conflict policy (M3): if
// dst already exists AND both files are append-only JSONL logs, union-merge them (merge.JSONL) so
// neither machine's appended lines are lost — the structural fix for claude-sync's last-writer-wins
// data loss. Every other case (a new file, or a non-.jsonl like memory/*.md) is last-write-wins:
// the incoming version replaces the local one.
func writeFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if strings.HasSuffix(dst, ".jsonl") {
		if local, err := os.ReadFile(dst); err == nil {
			incoming, err := os.ReadFile(src)
			if err != nil {
				return err
			}
			return writeAtomic(dst, func(w io.Writer) error {
				_, err := w.Write(merge.JSONL(local, incoming))
				return err
			})
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		// dst absent — fall through to a plain copy.
	}
	return copyFile(src, dst)
}

// copyFile atomically writes src's contents to dst.
func copyFile(src, dst string) error {
	return writeAtomic(dst, func(w io.Writer) error {
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(w, in)
		return err
	})
}

// writeAtomic writes via a temp file in dst's directory, then renames over dst. The rename is
// atomic on a single filesystem, so a crash mid-write can never leave a truncated session log —
// the integrity guarantee M3's merge exists to provide (a half-written history.jsonl would be
// worse than the clobber it replaces). fill streams the payload into the temp file.
func writeAtomic(dst string, fill func(io.Writer) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".mnemo-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := fill(tmp); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, dst)
}

// stampMtimeFromContent sets path's mtime to the newest timestamp recorded inside a JSONL log.
// `claude --resume` orders and dates sessions by file mtime, not by anything inside the transcript
// (verified against real data). But writeAtomic lays every file down via temp-file + rename, which
// stamps it with the restore time — so without this, a pull collapses every restored session to a
// single "just now" date and destroys their true recency order. The real last-activity time lives
// in the transcript's own `timestamp` fields, so we read it back. This also makes mtime a pure
// function of content: the same session pulled on any machine gets the same mtime. No-op (leaves
// the fresh mtime) when the file carries no parseable timestamp — e.g. an empty or non-transcript
// .jsonl. Cost is one extra pass over the just-written file, acceptable at pull frequency.
func stampMtimeFromContent(path string) {
	if t, ok := newestTimestamp(path); ok {
		_ = os.Chtimes(path, t, t)
	}
}

// newestTimestamp scans a JSONL file and returns the latest `timestamp` across all its lines.
// Scanning every line (not just the last) is deliberate: timestamps are not strictly ordered —
// summary and custom-title events interleave — so the newest can sit mid-file. Lines without a
// parseable timestamp are skipped. Uses a bufio.Reader (not Scanner) so a very long transcript
// line (embedded image/tool output) can't silently truncate the scan.
func newestTimestamp(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()
	var newest time.Time
	found := false
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if t, ok := lineTimestamp(line); ok && (!found || t.After(newest)) {
				newest, found = t, true
			}
		}
		if err != nil {
			break
		}
	}
	return newest, found
}

// lineTimestamp pulls the `timestamp` out of one JSONL record. Two encodings appear in Claude's
// data: transcripts use ISO-8601 strings, history.jsonl uses integer ms-epoch — both are handled.
func lineTimestamp(line []byte) (time.Time, bool) {
	var rec struct {
		Timestamp json.RawMessage `json:"timestamp"`
	}
	if json.Unmarshal(line, &rec) != nil || len(rec.Timestamp) == 0 {
		return time.Time{}, false
	}
	if rec.Timestamp[0] == '"' { // ISO-8601 string
		var s string
		if json.Unmarshal(rec.Timestamp, &s) != nil {
			return time.Time{}, false
		}
		t, err := time.Parse(time.RFC3339, s)
		return t, err == nil
	}
	var ms int64 // integer ms-epoch
	if json.Unmarshal(rec.Timestamp, &ms) != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(ms), true
}
