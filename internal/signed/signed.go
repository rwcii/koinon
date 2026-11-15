// Package signed reads, checks and signs the maintainer envelopes of #693: a JSON object
// signed with an Ed25519 key over its RFC 8785 bytes (docs/sprints/2026-11-14-signed-maintainer/
// decision.md, Decision 3). It knows no store, no route and no link.
package signed

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// Version is the envelope version and the domain of its signed bytes.
const Version = "koinon-maintainer-1"

// MaxEnvelopeBytes bounds one envelope as it arrives (decision.md, Answer shapes).
const MaxEnvelopeBytes = 512 << 10

// maxInteger is 2^53-1, the largest integer that every JSON reader keeps exactly.
const maxInteger = 1<<53 - 1

var (
	requestPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
	hexDigest      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Refusal is a refused envelope: the reason of signature_refused, or invalid_request.
type Refusal struct {
	Code   string
	Reason string
	Detail string
}

func (r Refusal) Error() string {
	if r.Detail != "" {
		return r.Code + ": " + r.Reason + ": " + r.Detail
	}
	return r.Code + ": " + r.Reason
}

func refuse(reason, format string, args ...any) error {
	return Refusal{Code: "signature_refused", Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// Envelope is one parsed envelope. Args keeps the decoded values: string, int64, bool, and
// map[string]any of those for changes.versions.
type Envelope struct {
	V       string
	Key     string
	Auth    string
	Target  string
	Op      string
	Args    map[string]any
	Request string
	Created int64
	Sig     string
	// signed is the exact signed bytes: the domain line and the RFC 8785 bytes without sig.
	signed []byte
}

// SignedBytes gives the bytes that the signature covers.
func (e Envelope) SignedBytes() []byte { return append([]byte(nil), e.signed...) }

// Digest gives the hex SHA-256 of the signed bytes, for receipts and authorization IDs.
func (e Envelope) Digest() string {
	sum := sha256.Sum256(e.signed)
	return hex.EncodeToString(sum[:])
}

// Parse reads one envelope: one JSON object with exactly the envelope's fields, no duplicate
// key and v1 values only. It checks the form, not the key or the signature.
func Parse(data []byte) (Envelope, error) {
	if len(data) > MaxEnvelopeBytes {
		return Envelope{}, refuse("version", "the envelope is larger than %d bytes", MaxEnvelopeBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	value, err := strict(dec, 0)
	if err != nil {
		return Envelope{}, refuse("version", "%v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return Envelope{}, refuse("version", "text after the envelope")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return Envelope{}, refuse("version", "the envelope is not a JSON object")
	}
	var e Envelope
	str := func(name string, required bool) (string, error) {
		raw, ok := object[name]
		if !ok {
			if required {
				return "", fmt.Errorf("%s is missing", name)
			}
			return "", nil
		}
		s, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("%s is not a string", name)
		}
		return s, nil
	}
	if e.V, err = str("v", true); err != nil {
		return Envelope{}, refuse("version", "%v", err)
	}
	if e.V != Version {
		return Envelope{}, refuse("version", "the version is not %s", Version)
	}
	if e.Op, err = str("op", true); err != nil {
		return Envelope{}, refuse("version", "%v", err)
	}
	enroll := e.Op == "enroll"
	allowed := map[string]bool{"v": true, "key": true, "target": true, "op": true, "args": true, "created": true, "sig": true}
	if !enroll {
		allowed["auth"], allowed["request"] = true, true
	}
	for name := range object {
		if !allowed[name] {
			return Envelope{}, refuse("version", "unknown field %q", name)
		}
	}
	for _, field := range []struct {
		name string
		to   *string
	}{{"key", &e.Key}, {"target", &e.Target}, {"sig", &e.Sig}} {
		if *field.to, err = str(field.name, true); err != nil {
			return Envelope{}, refuse("version", "%v", err)
		}
	}
	if !enroll {
		if e.Auth, err = str("auth", true); err != nil {
			return Envelope{}, refuse("version", "%v", err)
		}
		if e.Request, err = str("request", true); err != nil {
			return Envelope{}, refuse("version", "%v", err)
		}
		if !hexDigest.MatchString(e.Auth) {
			return Envelope{}, refuse("version", "auth is not a hex SHA-256")
		}
		if !requestPattern.MatchString(e.Request) {
			return Envelope{}, refuse("version", "request is not 16 to 128 of [A-Za-z0-9_-]")
		}
	}
	if !strings.HasPrefix(e.Key, "SHA256:") || !hexDigest.MatchString(e.Target) {
		return Envelope{}, refuse("version", "key or target has the wrong form")
	}
	created, ok := object["created"].(int64)
	if !ok {
		return Envelope{}, refuse("version", "created is not an integer")
	}
	e.Created = created
	args, ok := object["args"].(map[string]any)
	if !ok {
		return Envelope{}, refuse("version", "args is not an object")
	}
	for name, value := range args {
		if inner, nested := value.(map[string]any); nested {
			if e.Op != "changes" || name != "versions" {
				return Envelope{}, refuse("version", "args.%s is an object", name)
			}
			for topic, version := range inner {
				if _, ok := version.(string); !ok || topic == "" {
					return Envelope{}, refuse("version", "changes versions are topic to version strings")
				}
			}
		}
	}
	e.Args = args
	unsigned := map[string]any{}
	for name, value := range object {
		if name != "sig" {
			unsigned[name] = value
		}
	}
	canonical, err := Canonical(unsigned)
	if err != nil {
		return Envelope{}, refuse("version", "%v", err)
	}
	e.signed = append([]byte(Version+"\n"), canonical...)
	return e, nil
}

// strict decodes one JSON value with no duplicate key, no float, no null and no array.
func strict(dec *json.Decoder, depth int) (any, error) {
	if depth > 3 {
		return nil, errors.New("the envelope is nested too deep")
	}
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := token.(type) {
	case json.Delim:
		if t != '{' {
			return nil, errors.New("arrays are not v1 values")
		}
		object := map[string]any{}
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key := keyToken.(string)
			for _, r := range key {
				if r > 0x7e || r < 0x20 {
					return nil, errors.New("object keys are printable ASCII")
				}
			}
			if _, dup := object[key]; dup {
				return nil, fmt.Errorf("duplicate key %q", key)
			}
			value, err := strict(dec, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case string, bool:
		return t, nil
	case json.Number:
		if strings.ContainsAny(string(t), ".eE") {
			return nil, errors.New("floats are not v1 values")
		}
		n, err := t.Int64()
		if err != nil || n > maxInteger || n < -maxInteger {
			return nil, errors.New("integers are within 2^53-1")
		}
		return n, nil
	case nil:
		return nil, errors.New("null is not a v1 value")
	}
	return nil, fmt.Errorf("unexpected JSON token %v", token)
}

// Canonical gives the RFC 8785 bytes of a v1 value: objects with ASCII keys in order,
// strings, integers, booleans and, only inside a snapshot, arrays of strings.
func Canonical(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := canon(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func canon(b *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys) // ASCII keys: byte order is UTF-16 order
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			quote(b, k)
			b.WriteByte(':')
			if err := canon(b, x[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case []string:
		b.WriteByte('[')
		for i, s := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			quote(b, s)
		}
		b.WriteByte(']')
	case string:
		quote(b, x)
	case int64:
		fmt.Fprintf(b, "%d", x)
	case int:
		fmt.Fprintf(b, "%d", x)
	case bool:
		fmt.Fprintf(b, "%t", x)
	default:
		return fmt.Errorf("%T is not a v1 value", v)
	}
	return nil
}

// quote writes a JSON string as RFC 8785 does: the short escapes, \u00XX for other control
// characters, and every other character as itself.
func quote(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// KeyID gives SHA256: and the unpadded standard base64 of the public key's SHA-256.
func KeyID(public ed25519.PublicKey) string {
	sum := sha256.Sum256(public)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// Verify checks the envelope's signature with the public key.
func Verify(e Envelope, public ed25519.PublicKey) error {
	sig, err := base64.StdEncoding.DecodeString(e.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize || len(public) != ed25519.PublicKeySize {
		return refuse("signature", "the signature does not decode")
	}
	if !ed25519.Verify(public, e.signed, sig) {
		return refuse("signature", "the signature does not match")
	}
	return nil
}

// Enrollment is the checked content of an enrollment statement.
type Enrollment struct {
	Authorization string
	KeyID         string
	PublicKey     ed25519.PublicKey
	User          string
	Client        string
	Created       int64
	Target        string
}

var (
	userPattern   = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,31}$`)
	clientPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
)

// CheckEnrollment checks an enroll statement for this computer's pin at the time now: its
// public key matches its key ID and signs it, its user and client are valid, and it was made
// within 10 minutes of now (Decision 3.5). The authorization ID is the statement's digest.
func CheckEnrollment(e Envelope, pin string, now int64) (Enrollment, error) {
	if e.Op != "enroll" {
		return Enrollment{}, refuse("version", "the statement's op is not enroll")
	}
	if e.Target != pin {
		return Enrollment{}, refuse("target", "the statement is for another computer")
	}
	for name := range e.Args {
		if name != "user" && name != "client" && name != "public_key" {
			return Enrollment{}, refuse("version", "unknown enroll argument %q", name)
		}
	}
	user, _ := e.Args["user"].(string)
	client, _ := e.Args["client"].(string)
	encoded, _ := e.Args["public_key"].(string)
	if !userPattern.MatchString(user) || !clientPattern.MatchString(client) {
		return Enrollment{}, refuse("version", "user or client has the wrong form")
	}
	public, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(public) != ed25519.PublicKeySize {
		return Enrollment{}, refuse("version", "public_key is not a base64 Ed25519 public key")
	}
	if KeyID(public) != e.Key {
		return Enrollment{}, refuse("key_unknown", "public_key does not match key")
	}
	if err := Verify(e, public); err != nil {
		return Enrollment{}, err
	}
	if d := now - e.Created; d > 600 || d < -600 {
		return Enrollment{}, refuse("time", "the statement is not within 10 minutes of this computer's clock")
	}
	return Enrollment{Authorization: e.Digest(), KeyID: e.Key, PublicKey: ed25519.PublicKey(public), User: user, Client: client, Created: e.Created, Target: e.Target}, nil
}

// SnapshotDigest gives the hex SHA-256 of a decision item's snapshot (Decision 2.2).
func SnapshotDigest(reference string, revision int64, kind, title, where, context, recommendation, agentText string, commands []string) string {
	if commands == nil {
		commands = []string{}
	}
	canonical, _ := Canonical(map[string]any{
		"reference": reference, "revision": revision, "kind": kind, "title": title, "where": where,
		"context": context, "recommendation": recommendation, "agent_text": agentText, "commands": commands,
	})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// Sign gives the envelope, as JSON, for the unsigned fields, signed with the private key. Tests
// and clients use it; the daemon never holds a client key.
func Sign(fields map[string]any, private ed25519.PrivateKey) ([]byte, error) {
	canonical, err := Canonical(fields)
	if err != nil {
		return nil, err
	}
	sig := ed25519.Sign(private, append([]byte(Version+"\n"), canonical...))
	signed := map[string]any{}
	for k, v := range fields {
		signed[k] = v
	}
	signed["sig"] = base64.StdEncoding.EncodeToString(sig)
	return Canonical(signed)
}
