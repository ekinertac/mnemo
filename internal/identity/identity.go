// Package identity turns Claude Code's encoded project-dir names into machine-independent
// project identities and back. It is the heart of M2 (resume-aware cross-machine restore).
//
// Why it works purely on the ENCODED string (the `projects/<encoded>` dir name) and never on a
// decoded filesystem path: Claude encodes a cwd by replacing every non-alphanumeric character
// with '-' (verified against real data — see docs/superpowers/plans/2026-06-13-m2-project-identity.md
// step-0), which is irreversibly lossy (`age.sh` and `age-sh` collapse to the same string).
// Decoding is therefore impossible in general; tokenizing the encoded string is not. Identity =
// the encoded cwd with the machine-specific encoded-home prefix replaced by a token.
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
