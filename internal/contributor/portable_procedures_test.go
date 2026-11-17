package contributor

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"unicode"
)

// offeredProcedures are the skills of PDR 008 Decision 5 (#732), each with the SHA-256 of the
// text that the maintainer saw, with the changes that he approved for Koinon. Koinon
// offers them and adopts none, so it has no fact file for them. A later change of one of these
// skills updates its value here in the same pull request.
var offeredProcedures = map[string]string{
	"preflight":        "5eb0e8542131400fd58f71999a84a7f670bd99866c099c9e937b13a8474c2818",
	"provenance":       "d113f3be224ae8304cfcd9a59b002debe372e7d740f383e343a17c932f45685c",
	"refusal-audit":    "d0d53f9471b7fc36592c3786ea080dabc38074ce66fa193a2cb495114119fbb0",
	"standards-review": "fa3af14f7f0bc07c0c9786206732f9055a151dc35e08477327f45dda761e4194",
	"record":           "544e325d9fc613544f74fe5308b51233ab12bbce1fc68dd47dd3db2267bcc2b9",
	"sprint-closeout":  "c1527696198168ac09e692c5c7bba667dd17f96c95af8a82a1c8cc597767406f",
	"standards-audit":  "01e222ecdc02d18a69bb1ee3086f3097e8f3f3773fcb2df4964038616079f463",
}

// sourceNameHashes are the SHA-256 values of the lower-case names of the project that brought
// the procedures. The test holds only hashes, so that the public source does not name it.
var sourceNameHashes = map[string]bool{
	"91e776f8a587220fb5b68e64838e0757177e03f2ddbcc3d281f0400048e56dd8": true,
	"4fe7e10d7e05f16a05bf82c1223b20bd0c5335b77dcc52b8603bdb2abd8df78e": true,
}

// namedWords returns each word of text whose lower-case SHA-256 is in hashes. A word is a run
// of letters and digits, so a name inside a path or a hyphenated name is found.
func namedWords(text string, hashes map[string]bool) []string {
	var found []string
	for _, word := range strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		sum := sha256.Sum256([]byte(strings.ToLower(word)))
		if hashes[hex.EncodeToString(sum[:])] {
			found = append(found, word)
		}
	}
	return found
}

// The offered procedures are the text that the maintainer saw and name no source project
// (#732; PDR 008, Decision 5).
func TestOfferedProcedures(t *testing.T) {
	control := sha256.Sum256([]byte("examplecorp"))
	if found := namedWords("see /srv/ExampleCorp-tools/docs and examplecorp", map[string]bool{hex.EncodeToString(control[:]): true}); len(found) != 2 {
		t.Fatalf("the name scan missed its control: %q", found)
	}
	for name, want := range offeredProcedures {
		text := read(t, "agents/skills/"+name+"/SKILL.md")
		sum := sha256.Sum256([]byte(text))
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s: SHA-256 %s, want %s", name, got, want)
		}
		if found := namedWords(text, sourceNameHashes); len(found) != 0 {
			t.Errorf("%s names the source project: %q", name, found)
		}
		if !strings.Contains(text, "Koinon's procedure source is [PDR 008](") || strings.Contains(text, "<Koinon record for") {
			t.Errorf("%s: the procedure source is not PDR 008", name)
		}
	}
}
