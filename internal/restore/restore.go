// Package restore lays a restored Mnemo staging tree back into ~/.claude for THIS machine. It
// is the inverse of internal/stage's identity keying: by-id/<identity>/<rest> files are
// re-homed to ~/.claude/projects/<local-encoded-cwd>/<rest> so `claude --resume` finds them,
// while non-project data (history.jsonl, transcripts/, plans/, tasks/) lays straight back.
//
// Resolution precedence per identity: manifest override (this host) > home de-tokenization >
// absolute-as-is. Under-home identities always resolve (placement is harmless even if the local
// project dir doesn't exist yet), so M2 never drops a session. Conflict policy is tiered by file
// type at writeFile: .jsonl union-merges (M3), .md 3-way-merges against a caller-supplied common
// ancestor (BaseFunc), everything else is newer-mtime-wins.
//
// Related: internal/identity (the inverse mapping), internal/manifest (overrides),
// internal/command/pull.go (caller), docs/DESIGN.md §5.2.
package restore

import (
	"bufio"
	"bytes"
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
	LaidDown   int
	Unmapped   []string // identities with no resolvable local path on this host
	Conflicted []string // .md files that came out of a 3-way merge with conflict markers
	Skipped    []string // corrupt identities refused (e.g. an unexpanded ${HOME}); never laid down
}

// BaseFunc returns the common-ancestor bytes for a staged file, keyed by its staging-relative path
// (e.g. "by-id/home_-Code-foo/memory/n.md"), for use as the base of a 3-way .md merge. ok=false
// when no ancestor is recoverable (first sync from this machine, or a newly-created file). A nil
// BaseFunc disables 3-way merge, so .md files fall back to newer-wins — that is what pull passes.
type BaseFunc func(stagingRel string) (base []byte, ok bool)

// LayDown walks restoredRoot and materializes each file into claudeRoot. host/encodedHome and
// the manifest drive identity resolution for by-id/ entries. base supplies merge ancestors for
// .md 3-way merges (nil disables it — see BaseFunc).
func LayDown(restoredRoot, claudeRoot, host, encodedHome string, m *manifest.Manifest, base BaseFunc) (Report, error) {
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
		if err := writeFile(p, dst, relSlash, dstRel, base, &rep); err != nil {
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

// writeFile lays the incoming file (src) down at dst, resolving conflicts by type when dst already
// exists: .jsonl union-merges (append-only logs); .md 3-way-merges against the common ancestor
// from base (when available); everything else is newer-mtime-wins. A brand-new file is a plain
// copy. stagingRel keys the base lookup; dstRel is the ~/.claude-relative path used for reporting.
func writeFile(src, dst, stagingRel, dstRel string, base BaseFunc, rep *Report) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	existing, err := os.ReadFile(dst)
	if errors.Is(err, fs.ErrNotExist) {
		return copyFile(src, dst) // new file
	}
	if err != nil {
		return err
	}

	if strings.HasSuffix(dst, ".jsonl") {
		incoming, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		return writeAtomic(dst, func(w io.Writer) error {
			_, err := w.Write(merge.JSONL(existing, incoming))
			return err
		})
	}

	if strings.HasSuffix(dst, ".md") {
		incoming, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		// Fast path: an unchanged file is the common case (most memory notes don't move between
		// syncs). Skip the 3-way merge entirely — and, crucially, its base fetch, which is a remote
		// restic dump. Doing that dump per-file for hundreds of untouched .md files is what made
		// sync slow. Only diverging files pay for a base.
		if bytes.Equal(existing, incoming) {
			return nil
		}
		if base != nil {
			if baseBytes, ok := base(stagingRel); ok {
				merged, conflicts, mErr := merge.Text3Way(existing, baseBytes, incoming)
				if mErr == nil {
					if err := writeAtomic(dst, func(w io.Writer) error {
						_, err := w.Write(merged)
						return err
					}); err != nil {
						return err
					}
					if conflicts > 0 {
						rep.Conflicted = append(rep.Conflicted, dstRel)
					}
					return nil
				}
				// A genuine git failure (not a conflict) falls through to newer-wins rather than
				// aborting the whole lay-down for one file.
			}
		}
	}

	return newerWins(src, dst)
}

// newerWins keeps whichever of incoming (src) or local (dst) was modified most recently. restic
// restores each file with the mtime it was pushed with, so src's mtime is the incoming version's
// real authoring time. When incoming wins, its mtime is carried onto dst so the comparison stays
// meaningful on the next sync.
func newerWins(src, dst string) error {
	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	di, err := os.Stat(dst)
	if err != nil {
		return err
	}
	if !si.ModTime().After(di.ModTime()) {
		return nil // local newer or equal: keep it
	}
	if err := copyFile(src, dst); err != nil {
		return err
	}
	return os.Chtimes(dst, si.ModTime(), si.ModTime())
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
