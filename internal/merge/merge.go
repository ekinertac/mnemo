// Package merge reconciles append-only JSONL logs that diverged across machines — session
// transcripts and history.jsonl (DESIGN §5.3). It is the M3 answer to claude-sync's
// last-writer-wins clobbering: two machines that both appended to the same log must end up with
// the union of their lines, never one side overwriting the other.
//
// Strategy: longest common prefix of lines (the shared history), then the union of the remaining
// unique lines ordered by each line's `timestamp` field. It is pure, deterministic, idempotent on
// equal input, and — crucially — never drops a line. Claude's transcripts/history records carry an
// ISO-8601 `timestamp` (verified against real data); because that format is fixed-width UTC, plain
// lexicographic string order is chronological, so no time parsing is needed. Lines without a
// timestamp (e.g. the `permission-mode`/`file-history-snapshot` header lines at the very start of a
// transcript) normally sit in the common prefix; any that don't sort after the timestamped ones.
//
// Used by internal/restore: when lay-down finds an existing .jsonl at the destination, it merges
// rather than overwrites. restic dedups the rewritten blob, so re-storing a merged log is cheap.
package merge

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// JSONL merges two append-only JSONL logs. local is what's already on this machine; incoming is
// the version from the restored snapshot. The result is the union of their lines: the longest
// common prefix, then the remaining unique lines ordered by `timestamp`. Output is newline-
// terminated with no blank lines; equal input round-trips unchanged.
func JSONL(local, incoming []byte) []byte {
	a := splitLines(local)
	b := splitLines(incoming)

	// Longest common prefix — the shared history both machines agree on.
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}

	out := make([]string, 0, len(a)+len(b))
	out = append(out, a[:i]...)

	// Union of the divergent tails, deduped by exact line, preserving first-seen order (local
	// before incoming) so equal-timestamp lines have a deterministic order after the stable sort.
	seen := make(map[string]struct{}, len(a)+len(b))
	for _, s := range out {
		seen[s] = struct{}{}
	}
	var rest []string
	for _, s := range append(a[i:], b[i:]...) {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		rest = append(rest, s)
	}

	// Chronological merge of the tails. Stable so equal timestamps keep first-seen (local) order.
	sort.SliceStable(rest, func(x, y int) bool { return tsKey(rest[x]) < tsKey(rest[y]) })
	out = append(out, rest...)

	if len(out) == 0 {
		return []byte{}
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

// splitLines splits a JSONL blob into non-empty lines (drops blank lines and the trailing-newline
// artifact, so equal logs compare equal regardless of a final newline).
func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	raw := strings.Split(string(b), "\n")
	out := raw[:0]
	for _, l := range raw {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// tsKey returns a sortable key from a line's `timestamp`. Claude uses two encodings: transcripts
// carry an ISO-8601 string (`"2026-05-10T18:58:53.316Z"`, fixed-width UTC so lexicographic order is
// chronological), while history.jsonl carries an integer ms-since-epoch (`1762302821544`). The
// integer is zero-padded to 20 digits so lexicographic order matches numeric order (otherwise
// "10000" would sort before "2000"). Within a single file the encoding is homogeneous, so keys are
// never compared across the two forms. Lines that lack/!parse a timestamp get a high sentinel so
// they sort after real events (deterministically, via the stable sort).
func tsKey(line string) string {
	var e struct {
		Timestamp json.RawMessage `json:"timestamp"`
	}
	if json.Unmarshal([]byte(line), &e) != nil || len(e.Timestamp) == 0 {
		return "￿"
	}
	var s string
	if json.Unmarshal(e.Timestamp, &s) == nil && s != "" {
		return s // ISO-8601 string (transcripts)
	}
	var n int64
	if json.Unmarshal(e.Timestamp, &n) == nil {
		return fmt.Sprintf("%020d", n) // integer ms-epoch (history.jsonl)
	}
	return "￿"
}
