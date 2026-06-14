package merge

import "testing"

// lines builds a JSONL blob (trailing newline) from already-formatted JSON line strings.
func lines(ls ...string) []byte {
	out := ""
	for _, l := range ls {
		out += l + "\n"
	}
	return []byte(out)
}

const (
	hdr = `{"type":"permission-mode","mode":"default"}` // no timestamp — header line
	a1  = `{"timestamp":"2026-01-01T00:00:01Z","v":"A"}`
	b2  = `{"timestamp":"2026-01-01T00:00:02Z","v":"B"}`
	c3  = `{"timestamp":"2026-01-01T00:00:03Z","v":"C"}`
)

// Merging a log with itself returns it unchanged — idempotent, the common case where two
// machines are in sync.
func TestJSONLIdempotent(t *testing.T) {
	x := lines(a1, b2, c3)
	if got := JSONL(x, x); string(got) != string(x) {
		t.Errorf("JSONL(x,x) =\n%s\nwant\n%s", got, x)
	}
}

// Divergent tails after a shared prefix are re-interleaved by timestamp — the heart of §5.3.
func TestJSONLInterleavesByTimestamp(t *testing.T) {
	local := lines(a1, c3)    // this machine skipped B
	incoming := lines(a1, b2) // other machine had B
	want := lines(a1, b2, c3) // union, chronological
	if got := JSONL(local, incoming); string(got) != string(want) {
		t.Errorf("JSONL =\n%s\nwant\n%s", got, want)
	}
}

// A strict superset incoming keeps every line once (no duplication of the shared prefix).
func TestJSONLDedupesAndNeverDrops(t *testing.T) {
	local := lines(a1, b2)
	incoming := lines(a1, b2, c3)
	want := lines(a1, b2, c3)
	if got := JSONL(local, incoming); string(got) != string(want) {
		t.Errorf("JSONL =\n%s\nwant\n%s", got, want)
	}
}

// Both sides have a unique line at the same divergence point — union keeps both, ordered by ts.
func TestJSONLBothDiverge(t *testing.T) {
	local := lines(a1, c3)    // unique: c3
	incoming := lines(a1, b2) // unique: b2
	want := lines(a1, b2, c3)
	if got := JSONL(local, incoming); string(got) != string(want) {
		t.Errorf("JSONL =\n%s\nwant\n%s", got, want)
	}
}

// A no-timestamp header shared by both stays in the common prefix; timestamped events sort after.
func TestJSONLHeaderInCommonPrefix(t *testing.T) {
	local := lines(hdr, c3)
	incoming := lines(hdr, b2)
	want := lines(hdr, b2, c3)
	if got := JSONL(local, incoming); string(got) != string(want) {
		t.Errorf("JSONL =\n%s\nwant\n%s", got, want)
	}
}

// Empty on either side returns the other side verbatim (additive — never lose data).
func TestJSONLEmptySides(t *testing.T) {
	x := lines(a1, b2)
	if got := JSONL(nil, x); string(got) != string(x) {
		t.Errorf("JSONL(nil,x) =\n%s\nwant\n%s", got, x)
	}
	if got := JSONL(x, nil); string(got) != string(x) {
		t.Errorf("JSONL(x,nil) =\n%s\nwant\n%s", got, x)
	}
	if got := JSONL(nil, nil); len(got) != 0 {
		t.Errorf("JSONL(nil,nil) = %q, want empty", got)
	}
}

// Same timestamp on two distinct lines: deterministic order (local before incoming, stable).
func TestJSONLStableOnEqualTimestamp(t *testing.T) {
	lEq := `{"timestamp":"2026-01-01T00:00:05Z","v":"L"}`
	rEq := `{"timestamp":"2026-01-01T00:00:05Z","v":"R"}`
	local := lines(a1, lEq)
	incoming := lines(a1, rEq)
	want := lines(a1, lEq, rEq) // equal ts -> local first (stable)
	if got := JSONL(local, incoming); string(got) != string(want) {
		t.Errorf("JSONL =\n%s\nwant\n%s", got, want)
	}
}
