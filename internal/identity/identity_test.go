package identity

import (
	"strings"
	"testing"
)

func TestEncodeReplacesNonAlnumWithDash(t *testing.T) {
	cases := map[string]string{
		"/Users/ekinertac/Code/foo":     "-Users-ekinertac-Code-foo",
		"/Users/ekinertac/.dotfiles":    "-Users-ekinertac--dotfiles", // '/' and '.' both -> '-'
		"/Users/ekinertac/Code/age.sh":  "-Users-ekinertac-Code-age-sh",
		"ChatHumble":                    "ChatHumble", // case preserved
		"a b":                           "a-b",        // space -> '-'
		"café":                          "caf-",       // non-ASCII -> '-'
	}
	for in, want := range cases {
		if got := Encode(in); got != want {
			t.Errorf("Encode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEncodedHomeStripsWindowsDrive(t *testing.T) {
	// Unix home encodes directly; Windows %USERPROFILE% drops the drive (step-0 finding).
	if got := EncodedHome("/Users/ekinertac"); got != "-Users-ekinertac" {
		t.Errorf("unix EncodedHome = %q, want -Users-ekinertac", got)
	}
	if got := EncodedHome(`C:\Users\ekinertac`); got != "-Users-ekinertac" {
		t.Errorf("windows EncodedHome = %q, want -Users-ekinertac", got)
	}
	// Lowercase drive letters must strip too (the code handles c: as well as C:).
	if got := EncodedHome(`c:\Users\ekinertac`); got != "-Users-ekinertac" {
		t.Errorf("windows lowercase drive EncodedHome = %q, want -Users-ekinertac", got)
	}
}

func TestFromEncodedHomeAndAbs(t *testing.T) {
	home := "-Users-ekinertac"
	if got := FromEncoded("-Users-ekinertac-Code-foo", home); got != "home:-Code-foo" {
		t.Errorf("under-home = %q, want home:-Code-foo", got)
	}
	if got := FromEncoded("-opt-services-bar", home); got != "abs:-opt-services-bar" {
		t.Errorf("outside-home = %q, want abs:-opt-services-bar", got)
	}
	// The home dir itself (session opened at $HOME) -> empty tail.
	if got := FromEncoded("-Users-ekinertac", home); got != "home:" {
		t.Errorf("home root = %q, want home:", got)
	}
}

func TestFromEncodedPrefixBoundary(t *testing.T) {
	// "-Users-ekin" must NOT be treated as a prefix of "-Users-ekinside".
	if got := FromEncoded("-Users-ekinside-x", "-Users-ekin"); got != "abs:-Users-ekinside-x" {
		t.Errorf("boundary leak = %q, want abs:-Users-ekinside-x", got)
	}
}

func TestFromEncodedCaseInsensitiveHome(t *testing.T) {
	// macOS/Windows are case-insensitive; a differently-cased home prefix still tokenizes.
	if got := FromEncoded("-USERS-ekinertac-Code-foo", "-Users-ekinertac"); got != "home:-Code-foo" {
		t.Errorf("case-insensitive home = %q, want home:-Code-foo", got)
	}
}

func TestToEncodedRoundTrip(t *testing.T) {
	home := "-Users-ekin" // a DIFFERENT machine's home prefix
	enc, ok := ToEncoded("home:-Code-foo", home)
	if !ok || enc != "-Users-ekin-Code-foo" {
		t.Errorf("ToEncoded(home) = %q,%v want -Users-ekin-Code-foo,true", enc, ok)
	}
	enc, ok = ToEncoded("abs:-opt-bar", home)
	if !ok || enc != "-opt-bar" {
		t.Errorf("ToEncoded(abs) = %q,%v want -opt-bar,true", enc, ok)
	}
	// home: at the home root resolves back to the bare encoded home (symmetry with FromEncoded).
	if enc, ok := ToEncoded("home:", home); !ok || enc != "-Users-ekin" {
		t.Errorf("ToEncoded(home root) = %q,%v want -Users-ekin,true", enc, ok)
	}
	if _, ok := ToEncoded("garbage-no-scheme", home); ok {
		t.Error("malformed identity should return ok=false")
	}
}

// PathSafe must produce a directory-name-safe form (no ':' — illegal on NTFS) that round-trips
// back to the canonical identity. Only the scheme ':' is touched; the encoded tail (already
// [A-Za-z0-9-]) is untouched, and '_' never appears in an identity so the mapping is unambiguous.
func TestPathSafeRoundTrip(t *testing.T) {
	cases := map[Identity]string{
		"home:-Code-foo":       "home_-Code-foo",
		"abs:-opt-services-bar": "abs_-opt-services-bar",
		"home:":                "home_", // home root
	}
	for id, wantSeg := range cases {
		seg := PathSafe(id)
		if seg != wantSeg {
			t.Errorf("PathSafe(%q) = %q, want %q", id, seg, wantSeg)
		}
		if strings.ContainsRune(seg, ':') {
			t.Errorf("PathSafe(%q) still contains ':' — illegal on NTFS", id)
		}
		if back := FromPathSafe(seg); back != id {
			t.Errorf("FromPathSafe(%q) = %q, want %q", seg, back, id)
		}
	}
}
