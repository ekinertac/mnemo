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
	id, tail := rest[:slash], rest[slash+1:]
	localEncoded, ok := ResolveLocal(identity.Identity(id), host, encodedHome, m)
	if !ok {
		rep.Unmapped = append(rep.Unmapped, id)
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
		return identity.Encode(stripDrive(ov)), true
	}
	return identity.ToEncoded(id, encodedHome)
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
