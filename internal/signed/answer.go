package signed

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// AnswerVersion is the version of a signed answer and the domain of its signed bytes (#696,
// docs/sprints/2026-11-22-signed-answers/contract.md, section 3).
const AnswerVersion = "koinon-answer-2"

// The signers of an answer: the computer that carries out the action, or the node for its own
// claims (contract.md, section 4).
const (
	SignerTarget = "target"
	SignerNode   = "node"
)

// NodeClaims are the codes that a node may sign as its own, each with ok false.
var NodeClaims = map[string]bool{
	"computer_offline": true, "outcome_unknown": true, "signature_refused": true, "invalid_request": true,
	"too_large": true, "link_operation_unknown": true, "link_pair_not_found": true, "link_remote_required": true,
	"link_frame_too_large": true,
}

// Answer is one signed answer to a version 2 envelope.
type Answer struct {
	V         string `json:"v"`
	Signer    string `json:"signer"`
	Pin       string `json:"pin"`
	PublicKey string `json:"public_key"`
	Request   string `json:"request"`
	Status    int64  `json:"status"`
	Answer    string `json:"answer"`
	Sig       string `json:"sig"`
}

// fields gives the answer's values without sig, for its signed bytes.
func (a Answer) fields() map[string]any {
	return map[string]any{"v": a.V, "signer": a.Signer, "pin": a.Pin, "public_key": a.PublicKey,
		"request": a.Request, "status": a.Status, "answer": a.Answer}
}

// signedBytes gives the bytes that the answer's signature covers.
func (a Answer) signedBytes() ([]byte, error) {
	canonical, err := Canonical(a.fields())
	if err != nil {
		return nil, err
	}
	return append([]byte(AnswerVersion+"\n"), canonical...), nil
}

// LinkPublicKey gives a link key's public key as standard base64 DER SubjectPublicKeyInfo, and
// its pin, the lower-case hex SHA-256 of that DER.
func LinkPublicKey(public ed25519.PublicKey) (string, string, error) {
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(der)
	return base64.StdEncoding.EncodeToString(der), hex.EncodeToString(sum[:]), nil
}

// SignAnswer signs the exact answer text with a link key for the request digest.
func SignAnswer(signer string, private ed25519.PrivateKey, request string, status int, answer []byte) (Answer, error) {
	public, pin, err := LinkPublicKey(private.Public().(ed25519.PublicKey))
	if err != nil {
		return Answer{}, err
	}
	a := Answer{V: AnswerVersion, Signer: signer, Pin: pin, PublicKey: public, Request: request, Status: int64(status), Answer: string(answer)}
	data, err := a.signedBytes()
	if err != nil {
		return Answer{}, err
	}
	a.Sig = base64.StdEncoding.EncodeToString(ed25519.Sign(private, data))
	return a, nil
}

// ParseAnswer reads one signed answer: one JSON object with exactly its eight fields, no
// duplicate key, status an integer and the others strings (contract.md, section 3.1). It checks
// the form, not the signature.
func ParseAnswer(data []byte) (Answer, error) {
	if err := ValidText(data); err != nil {
		return Answer{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	value, err := strict(dec, 0)
	if err != nil {
		return Answer{}, fmt.Errorf("the answer is not one signed object: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return Answer{}, errors.New("text after the answer")
	}
	object, ok := value.(map[string]any)
	if !ok || len(object) != 8 {
		return Answer{}, errors.New("the answer does not have exactly its eight fields")
	}
	var a Answer
	for name, to := range map[string]*string{"v": &a.V, "signer": &a.Signer, "pin": &a.Pin, "public_key": &a.PublicKey,
		"request": &a.Request, "answer": &a.Answer, "sig": &a.Sig} {
		if *to, ok = object[name].(string); !ok {
			return Answer{}, fmt.Errorf("%s is missing or not a string", name)
		}
	}
	if a.Status, ok = object["status"].(int64); !ok || a.Status < 200 || a.Status > 599 {
		return Answer{}, errors.New("status is not an integer from 200 to 599")
	}
	return a, nil
}

// IsAnswer reports whether data has the form of a signed answer.
func IsAnswer(data []byte) bool {
	_, err := ParseAnswer(data)
	return err == nil
}

// Expect is what a client knows when it checks an answer: the digest of the envelope that it
// sent, the envelope's target pin, and the pin of the node that it sends through.
type Expect struct {
	Request string
	Target  string
	Node    string
}

// Verified is the checked inner answer: the exact JSON text, its status, ok and code.
type Verified struct {
	Signer string
	Status int
	OK     bool
	Code   string
	Text   []byte
}

// ErrNotVerified refuses an answer that a client must show as "answer not verified".
var ErrNotVerified = errors.New("answer not verified")

func notVerified(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrNotVerified, fmt.Sprintf(format, args...))
}

// VerifyAnswer checks a signed answer as a client does (contract.md, section 3.4): the form,
// the signer's pin and role, the public key's hash, the request digest, the signature, and the
// inner answer, which for the node is one of its claims.
func VerifyAnswer(data []byte, x Expect) (Verified, error) {
	a, err := ParseAnswer(data)
	if err != nil {
		return Verified{}, notVerified("%v", err)
	}
	if a.V != AnswerVersion {
		return Verified{}, notVerified("the version is not %s", AnswerVersion)
	}
	switch a.Signer {
	case SignerTarget:
		if a.Pin != x.Target {
			return Verified{}, notVerified("the target's answer is signed for another pin")
		}
	case SignerNode:
		if a.Pin != x.Node {
			return Verified{}, notVerified("the node's claim is signed for another pin")
		}
	default:
		return Verified{}, notVerified("unknown signer")
	}
	der, err := base64.StdEncoding.DecodeString(a.PublicKey)
	if err != nil {
		return Verified{}, notVerified("public_key does not decode")
	}
	if sum := sha256.Sum256(der); hex.EncodeToString(sum[:]) != a.Pin {
		return Verified{}, notVerified("public_key does not hash to pin")
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	public, ok := parsed.(ed25519.PublicKey)
	if err != nil || !ok {
		return Verified{}, notVerified("public_key is not an Ed25519 key")
	}
	if a.Request != x.Request {
		return Verified{}, notVerified("the answer is for another request")
	}
	sig, err := base64.StdEncoding.DecodeString(a.Sig)
	signed, cerr := a.signedBytes()
	if err != nil || cerr != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(public, signed, sig) {
		return Verified{}, notVerified("the signature does not match")
	}
	ok, code, err := innerAnswer([]byte(a.Answer))
	if err != nil {
		return Verified{}, notVerified("%v", err)
	}
	if ok != (a.Status == 200) {
		return Verified{}, notVerified("ok does not agree with status")
	}
	if a.Signer == SignerNode && (ok || !NodeClaims[code] || code == "signature_refused" && refusalReason([]byte(a.Answer)) != "target") {
		return Verified{}, notVerified("the node signed an answer that is not one of its claims")
	}
	return Verified{Signer: a.Signer, Status: int(a.Status), OK: ok, Code: code, Text: []byte(a.Answer)}, nil
}

// innerAnswer reads the answer text: one JSON object with no duplicate top-level key, a boolean
// ok and, when ok is false, a code.
func innerAnswer(data []byte) (bool, string, error) {
	if err := ValidText(data); err != nil || !json.Valid(data) {
		return false, "", errors.New("the answer text is not valid JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return false, "", errors.New("the answer text is not one JSON object")
	}
	seen := map[string]json.RawMessage{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return false, "", err
		}
		key, _ := t.(string)
		if _, dup := seen[key]; dup {
			return false, "", fmt.Errorf("the answer text has the key %q twice", key)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return false, "", err
		}
		seen[key] = raw
	}
	// ok is a JSON boolean: the decoder would take null as false.
	var ok bool
	switch string(seen["ok"]) {
	case "true":
		ok = true
	case "false":
	default:
		return false, "", errors.New("the answer text has no boolean ok")
	}
	var code string
	if !ok && (json.Unmarshal(seen["code"], &code) != nil || code == "") {
		return false, "", errors.New("a refusal has no code")
	}
	return ok, code, nil
}

// refusalReason gives details.reason of a refusal's text.
func refusalReason(data []byte) string {
	var refusal struct {
		Details struct {
			Reason string `json:"reason"`
		} `json:"details"`
	}
	json.Unmarshal(data, &refusal)
	return refusal.Details.Reason
}
