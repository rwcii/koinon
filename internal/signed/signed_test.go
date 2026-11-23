package signed

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// vectors reads testdata/vectors.txt, the shared test vectors of the #693 contract.
func vectors(t *testing.T) (map[string]string, []map[string]string) {
	t.Helper()
	data, err := os.ReadFile("testdata/vectors.txt")
	if err != nil {
		t.Fatal(err)
	}
	head := map[string]string{}
	var envelopes []map[string]string
	var current map[string]string
	for _, line := range strings.Split(string(data), "\n") {
		name, value, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		if name == "op" {
			current = map[string]string{}
			envelopes = append(envelopes, current)
		}
		if current != nil {
			current[name] = value
		} else {
			head[strings.Fields(name)[0]] = value
		}
	}
	return head, envelopes
}

func TestVectors(t *testing.T) {
	head, envelopes := vectors(t)
	seed, _ := hex.DecodeString(head["seed_hex"])
	private := ed25519.NewKeyFromSeed(seed)
	public := private.Public().(ed25519.PublicKey)
	if hex.EncodeToString(public) != head["public_key_hex"] || KeyID(public) != head["key_id"] {
		t.Fatalf("key: %x %s", public, KeyID(public))
	}
	if got := SnapshotDigest("VEC-1", 3, "decision", "Approve the test vector", "docs/test", "- Line one\n- Line two", "Approve.", "", nil); got != head["snapshot_sha256"] {
		t.Fatalf("snapshot digest %s", got)
	}
	if got := SnapshotDigest("VEC-1", 3, "decision", "Approve the test vector", "docs/test", "- Line one\n- Line two", "Approve.", "", []string{"koinon version", "git status --short"}); got != head["snapshot2_sha256"] {
		t.Fatalf("snapshot2 digest %s", got)
	}
	if len(envelopes) != 2 {
		t.Fatalf("envelopes: %d", len(envelopes))
	}
	var auth string
	for _, v := range envelopes {
		var object map[string]any
		json.Unmarshal([]byte(v["canonical"]), &object)
		object["sig"] = v["sig_b64"]
		data, _ := json.Marshal(object)
		e, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", v["op"], err)
		}
		if hex.EncodeToString(e.SignedBytes()) != v["signed_bytes_hex"] {
			t.Fatalf("%s signed bytes differ", v["op"])
		}
		if err := Verify(e, public); err != nil {
			t.Fatalf("%s: %v", v["op"], err)
		}
		if v["op"] == "enroll" {
			en, err := CheckEnrollment(e, e.Target, e.Created+60)
			if err != nil || en.Authorization != head["auth"] || en.User != "maintainer" || en.Client != "koinon-voice" {
				t.Fatalf("enrollment: %+v %v", en, err)
			}
			auth = en.Authorization
		} else if e.Auth != auth || e.Args["snapshot"] != head["snapshot_sha256"] {
			t.Fatalf("verdict auth %s snapshot %v", e.Auth, e.Args["snapshot"])
		}
		// Sign gives the same bytes from the same fields.
		delete(object, "sig")
		resigned, err := Sign(normalize(object), private)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Parse(resigned)
		if err != nil || again.Sig != v["sig_b64"] {
			t.Fatalf("%s: Sign differs: %v", v["op"], err)
		}
	}
}

// normalize turns float64 numbers from encoding/json into int64.
func normalize(v map[string]any) map[string]any {
	out := map[string]any{}
	for k, x := range v {
		switch y := x.(type) {
		case float64:
			out[k] = int64(y)
		case map[string]any:
			out[k] = normalize(y)
		default:
			out[k] = x
		}
	}
	return out
}

func TestParseRefusals(t *testing.T) {
	head, _ := vectors(t)
	seed, _ := hex.DecodeString(head["seed_hex"])
	private := ed25519.NewKeyFromSeed(seed)
	base := func() map[string]any {
		return map[string]any{"v": Version, "key": head["key_id"], "auth": head["auth"], "target": strings.Repeat("ab", 32),
			"op": "inbox", "args": map[string]any{"after": int64(0), "limit": int64(10)}, "request": "request-0000000001", "created": int64(1794699000)}
	}
	good, err := Sign(base(), private)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(good); err != nil {
		t.Fatalf("good envelope: %v", err)
	}
	cases := map[string]string{
		"unknown field": strings.Replace(string(good), `"v":`, `"x":1,"v":`, 1),
		"duplicate key": strings.Replace(string(good), `"v":`, `"op":"inbox","v":`, 1),
		"float":         strings.Replace(string(good), `"after":0`, `"after":0.5`, 1),
		"null":          strings.Replace(string(good), `"after":0`, `"after":null`, 1),
		"array":         strings.Replace(string(good), `"after":0`, `"after":[0]`, 1),
		"big integer":   strings.Replace(string(good), `"after":0`, `"after":9007199254740992`, 1),
		"version":       strings.Replace(string(good), Version, "koinon-maintainer-3", 1),
		"short request": strings.Replace(string(good), "request-0000000001", "short", 1),
		"two objects":   string(good) + "{}",
		"too large":     strings.Replace(string(good), `"after":0`, `"after":0,"pad":"`+strings.Repeat("a", MaxEnvelopeBytes)+`"`, 1),
		// RFC 8785 section 3.2.2.2: invalid Unicode is an error, never U+FFFD (review K2-F1).
		"lone high surrogate": strings.Replace(string(good), `"after":0`, `"after":0,"s":"\ud800"`, 1),
		"lone low surrogate":  strings.Replace(string(good), `"after":0`, `"after":0,"s":"\udead"`, 1),
		"high then text":      strings.Replace(string(good), `"after":0`, `"after":0,"s":"\ud800x"`, 1),
		"high then high":      strings.Replace(string(good), `"after":0`, `"after":0,"s":"\ud800\ud800"`, 1),
		"high at the end":     strings.Replace(string(good), `"after":0`, `"after":0,"s":"x\uD800"`, 1),
		"invalid byte":        strings.Replace(string(good), `"after":0`, "\"after\":0,\"s\":\"\xff\"", 1),
	}
	for name, data := range cases {
		var refusal Refusal
		if _, err := Parse([]byte(data)); !errors.As(err, &refusal) || refusal.Code != "signature_refused" {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Valid text that looks like those forms is kept.
	for name, value := range map[string]string{"pair": `\ud83d\ude00`, "escaped backslash": `\\ud800`, "replacement character": "\ufffd"} {
		if _, err := Parse([]byte(strings.Replace(string(good), `"after":0`, `"after":0,"s":"`+value+`"`, 1))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := Canonical(string([]byte{0xff})); err == nil {
		t.Error("Canonical took invalid UTF-8")
	}
	if _, err := Canonical(map[string]any{"a": []string{"ok", "\xff"}}); err == nil {
		t.Error("Canonical took invalid UTF-8 in an array")
	}
	// An enroll statement carries no auth or request.
	enroll := base()
	enroll["op"], enroll["args"] = "enroll", map[string]any{"user": "maintainer", "client": "voice", "public_key": head["public_key_b64"]}
	data, _ := Sign(enroll, private)
	if _, err := Parse(data); err == nil {
		t.Error("enroll with auth accepted")
	}
	// A changed argument breaks the signature.
	e, _ := Parse([]byte(strings.Replace(string(good), `"limit":10`, `"limit":11`, 1)))
	if err := Verify(e, private.Public().(ed25519.PublicKey)); err == nil {
		t.Error("changed argument verified")
	}
}

func TestCheckEnrollmentRefusals(t *testing.T) {
	head, envelopes := vectors(t)
	var object map[string]any
	json.Unmarshal([]byte(envelopes[0]["canonical"]), &object)
	object["sig"] = envelopes[0]["sig_b64"]
	data, _ := json.Marshal(object)
	e, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CheckEnrollment(e, strings.Repeat("cd", 32), e.Created); err == nil {
		t.Error("another computer's statement accepted")
	}
	if _, err := CheckEnrollment(e, e.Target, e.Created+601); err == nil {
		t.Error("an 11-minute-old statement accepted")
	}
	seed, _ := hex.DecodeString(head["seed_hex"])
	other := ed25519.NewKeyFromSeed(append(seed[1:], 0x21))
	fields := normalize(object)
	delete(fields, "sig")
	fields["args"].(map[string]any)["public_key"] = head["public_key_b64"]
	resigned, _ := Sign(fields, other)
	e2, _ := Parse(resigned)
	if _, err := CheckEnrollment(e2, e2.Target, e2.Created); err == nil {
		t.Error("a statement signed by another key accepted")
	}
}
