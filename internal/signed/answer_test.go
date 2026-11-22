package signed

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// vectors2 reads testdata/vectors2.txt, the shared vectors of #696
// (docs/sprints/2026-11-22-signed-answers/contract.md, section 5): a head of keys and pins,
// then blocks headed "envelope NAME" or "answer NAME".
func vectors2(t *testing.T) (map[string]string, map[string]map[string]string) {
	t.Helper()
	data, err := os.ReadFile("testdata/vectors2.txt")
	if err != nil {
		t.Fatal(err)
	}
	head, blocks := map[string]string{}, map[string]map[string]string{}
	var current map[string]string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "envelope ") || strings.HasPrefix(line, "answer ") {
			current = map[string]string{}
			blocks[line] = current
			continue
		}
		name, value, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		if current != nil {
			current[name] = value
		} else {
			head[name] = value
		}
	}
	return head, blocks
}

func seedFrom(first byte) ed25519.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = first + byte(i)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func TestAnswerVectors(t *testing.T) {
	head, blocks := vectors2(t)
	maintainer, target, node := seedFrom(0x01), seedFrom(0x21), seedFrom(0x41)
	for _, k := range []struct {
		key       ed25519.PrivateKey
		pin, spki string
	}{{target, "target_pin", "target_spki_b64"}, {node, "node_pin", "node_spki_b64"}} {
		spki, pin, err := LinkPublicKey(k.key.Public().(ed25519.PublicKey))
		if err != nil || pin != head[k.pin] || spki != head[k.spki] {
			t.Fatalf("%s: %s %s %v", k.pin, pin, spki, err)
		}
	}
	// Each envelope: Sign gives its signature from its fields, and Parse gives its digest.
	digests := map[string]string{}
	for name, b := range blocks {
		op, isEnvelope := strings.CutPrefix(name, "envelope ")
		if !isEnvelope {
			continue
		}
		var fields map[string]any
		json.Unmarshal([]byte(b["canonical"]), &fields)
		data, err := Sign(normalize(fields), maintainer)
		if err != nil {
			t.Fatal(err)
		}
		e, err := Parse(data)
		if err != nil || e.V != Version2 || e.Sig != b["sig_b64"] || e.Digest() != b["request_digest"] {
			t.Fatalf("%s: %+v %v", name, e, err)
		}
		if err := Verify(e, maintainer.Public().(ed25519.PublicKey)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		digests[op] = e.Digest()
	}
	// Each answer: SignAnswer gives its signature, and VerifyAnswer accepts it for its envelope.
	for name, b := range blocks {
		if !strings.HasPrefix(name, "answer ") {
			continue
		}
		var fields map[string]any
		json.Unmarshal([]byte(b["canonical"]), &fields)
		signer, _ := fields["signer"].(string)
		key, request := target, digests["room_post"]
		switch {
		case signer == SignerNode || strings.Contains(name, "session"):
			key = node
		}
		for op, digest := range digests {
			if strings.HasPrefix(strings.TrimPrefix(name, "answer "), op+" ") {
				request = digest
			}
		}
		a, err := SignAnswer(signer, key, request, int(fields["status"].(float64)), []byte(fields["answer"].(string)))
		if err != nil || a.Sig != b["sig_b64"] {
			t.Fatalf("%s: %s %v", name, a.Sig, err)
		}
		data, _ := json.Marshal(a)
		x := Expect{Request: request, Target: head["target_pin"], Node: head["node_pin"]}
		if strings.Contains(name, "session") {
			x.Target = head["node_pin"]
		}
		v, err := VerifyAnswer(data, x)
		if err != nil || v.Signer != signer || string(v.Text) != fields["answer"] {
			t.Fatalf("%s: %+v %v", name, v, err)
		}
	}
}

// The refused vectors of contract.md, section 5: each change fails the client's check.
func TestAnswerRefusals(t *testing.T) {
	head, _ := vectors2(t)
	target, node := seedFrom(0x21), seedFrom(0x41)
	request := strings.Repeat("ab", 32)
	x := Expect{Request: request, Target: head["target_pin"], Node: head["node_pin"]}
	ok := `{"ok":true,"result":{"recipients":[{"name":"agent-a","state":"sent"}],"note":"<\"quoted\" \\ \u0001 é>"}}`
	good, err := SignAnswer(SignerTarget, target, request, 200, []byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	encode := func(a Answer) []byte { data, _ := json.Marshal(a); return data }
	if _, err := VerifyAnswer(encode(good), x); err != nil {
		t.Fatalf("good: %v", err)
	}
	offline := `{"ok":false,"code":"computer_offline","error":"x","details":{"target":"` + head["target_pin"] + `"}}`
	claim, _ := SignAnswer(SignerNode, node, request, 409, []byte(offline))
	if v, err := VerifyAnswer(encode(claim), x); err != nil || v.Code != "computer_offline" {
		t.Fatalf("node claim: %+v %v", v, err)
	}
	nodeSuccess, _ := SignAnswer(SignerNode, node, request, 200, []byte(ok))
	nodeOther, _ := SignAnswer(SignerNode, node, request, 409, []byte(`{"ok":false,"code":"stale_revision"}`))
	nodeVersion, _ := SignAnswer(SignerNode, node, request, 409, []byte(`{"ok":false,"code":"signature_refused","details":{"reason":"version"}}`))
	asTarget, _ := SignAnswer(SignerTarget, node, request, 200, []byte(ok))
	mutate := func(f func(*Answer)) []byte { a := good; f(&a); return encode(a) }
	raw := func(f func(map[string]any)) []byte {
		var m map[string]any
		json.Unmarshal(encode(good), &m)
		f(m)
		data, _ := json.Marshal(m)
		return data
	}
	for name, data := range map[string][]byte{
		"another request":          mutate(func(a *Answer) { a.Request = strings.Repeat("cd", 32) }),
		"another pin":              mutate(func(a *Answer) { a.Pin = head["node_pin"] }),
		"signer changed":           mutate(func(a *Answer) { a.Signer = SignerNode }),
		"status changed":           mutate(func(a *Answer) { a.Status = 409 }),
		"answer byte changed":      mutate(func(a *Answer) { a.Answer = strings.Replace(a.Answer, "agent-a", "agent-b", 1) }),
		"key not the pin":          mutate(func(a *Answer) { a.PublicKey = base64.StdEncoding.EncodeToString([]byte("synthetic")) }),
		"node success":             encode(nodeSuccess),
		"node other code":          encode(nodeOther),
		"node version claim":       encode(nodeVersion),
		"target with node key":     encode(asTarget),
		"missing field":            raw(func(m map[string]any) { delete(m, "signer") }),
		"extra field":              raw(func(m map[string]any) { m["extra"] = "x" }),
		"duplicate field":          []byte(strings.Replace(string(encode(good)), `{"v":`, `{"v":"koinon-answer-2","v":`, 1)),
		"unsigned version refusal": []byte(`{"ok":false,"code":"signature_refused","error":"x","details":{"reason":"version"}}`),
	} {
		if _, err := VerifyAnswer(data, x); !errors.Is(err, ErrNotVerified) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// A version 2 enrollment statement is refused; Sign signs version 2 with its own domain.
	if _, err := Parse([]byte(`{"v":"koinon-maintainer-2","op":"enroll"}`)); err == nil || !strings.Contains(err.Error(), "enrollment") {
		t.Fatalf("v2 enroll: %v", err)
	}
}
