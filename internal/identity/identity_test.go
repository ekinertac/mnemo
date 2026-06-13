package identity

import "testing"

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
