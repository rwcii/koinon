package link

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func privateRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestIdentityCreateReuseAndRenewKeepsPin(t *testing.T) {
	root := privateRoot(t)
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	first, err := LoadIdentity(root, now)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, KeyFile))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("key file %v %v", err, info.Mode())
	}
	again, err := LoadIdentity(root, now.Add(time.Hour))
	if err != nil || again.Pin != first.Pin || !bytes.Equal(again.Certificate.Certificate[0], first.Certificate.Certificate[0]) {
		t.Fatalf("reuse: %v", err)
	}
	// Near the end of its validity the certificate is renewed with the same key: same pin.
	late := first.Certificate.Leaf.NotAfter.Add(-24 * time.Hour)
	renewed, err := LoadIdentity(root, late)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Pin != first.Pin || bytes.Equal(renewed.Certificate.Certificate[0], first.Certificate.Certificate[0]) || !renewed.Certificate.Leaf.NotAfter.After(late.Add(renewWindow)) {
		t.Fatal("renewal did not keep the key or did not renew")
	}
	if !ValidPin(first.Pin) {
		t.Fatalf("pin %q", first.Pin)
	}
}

func TestIdentityRefusesForeignFiles(t *testing.T) {
	root := privateRoot(t)
	path := filepath.Join(root, KeyFile)
	if err := os.WriteFile(path, []byte("not pem"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(root, time.Now()); err == nil || !strings.HasPrefix(err.Error(), "link_key_invalid") {
		t.Fatalf("garbage: %v", err)
	}
	// A certificate of another key is refused, never silently replaced.
	other := privateRoot(t)
	a, _ := LoadIdentity(other, time.Now())
	b, _ := os.ReadFile(filepath.Join(other, KeyFile))
	third := privateRoot(t)
	c, _ := LoadIdentity(third, time.Now())
	cdata, _ := os.ReadFile(filepath.Join(third, KeyFile))
	certA := b[:bytes.Index(b, []byte("-----BEGIN PRIVATE KEY"))]
	keyC := cdata[bytes.Index(cdata, []byte("-----BEGIN PRIVATE KEY")):]
	if err := os.WriteFile(path, append(append([]byte(nil), certA...), keyC...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(root, time.Now()); err == nil {
		t.Fatalf("mixed key and certificate accepted (%s %s)", a.Pin[:8], c.Pin[:8])
	}
	// A link instead of a regular file is refused.
	os.Remove(path)
	if err := os.Symlink(filepath.Join(other, KeyFile), path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(root, time.Now()); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestNames(t *testing.T) {
	for _, ok := range []string{"devbox", "a", "mac-mini-2", "devbox.example.internal", strings.Repeat("a", 63), strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 61)} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Devbox", "dev_box", "-devbox", "devbox-", "dev..box", ".devbox", strings.Repeat("a", 64), strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 62), "dev box", "devbox@x", "dévbox"} {
		if ValidateName(bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if got := NormalizeName(" DevBox.Example.Internal. "); got != "devbox.example.internal" {
		t.Fatalf("normalize %q", got)
	}
}

func TestShortAndLongName(t *testing.T) {
	defer func(h func() (string, error), l func(context.Context, string) (string, error)) { hostname, lookupCNAME = h, l }(hostname, lookupCNAME)
	cases := []struct{ host, cname, short, long string }{
		{"DevBox", "devbox.example.internal.", "devbox", "devbox.example.internal"},
		{"devbox.example.internal", "", "devbox", "devbox.example.internal"},
		{"MacBook-Pro.local.", "", "macbook-pro", "macbook-pro.local"},
		{"devbox", "devbox", "devbox", ""},
		{"dev_box", "", "", ""},
	}
	for _, c := range cases {
		hostname = func() (string, error) { return c.host, nil }
		lookupCNAME = func(context.Context, string) (string, error) {
			if c.cname == "" {
				return "", errors.New("no answer")
			}
			return c.cname, nil
		}
		if got := ShortName(); got != c.short {
			t.Errorf("%q short %q", c.host, got)
		}
		if got := LongName(context.Background()); got != c.long {
			t.Errorf("%q long %q", c.host, got)
		}
	}
	hostname = func() (string, error) { return "", errors.New("no host name") }
	if ShortName() != "" || LongName(context.Background()) != "" {
		t.Fatal("names without a host name")
	}
}

func testInvitation(t *testing.T) Invitation {
	t.Helper()
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	return Invitation{Version: 1, DNSName: "devbox.example-tailnet.ts.net", IPs: []string{"100.64.0.10", "fd7a:115c:a1e0::a"}, Port: DefaultPort, Pin: strings.Repeat("ab", 32), Name: "devbox", Secret: secret}
}

func TestInvitationRoundTripAndRefusals(t *testing.T) {
	i := testInvitation(t)
	text, err := i.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseInvitation("\n  " + text + "\r\n")
	if err != nil || got.Secret != i.Secret || got.Pin != i.Pin || len(got.IPs) != 2 {
		t.Fatalf("round trip %v %+v", err, got)
	}
	mutate := func(f func(*Invitation)) string {
		c := i
		c.IPs = append([]string(nil), i.IPs...)
		f(&c)
		data, _ := json.Marshal(c)
		return InvitationPrefix + base64.RawURLEncoding.EncodeToString(data)
	}
	bad := map[string]string{
		"prefix":        strings.TrimPrefix(text, InvitationPrefix),
		"truncated":     text[:len(text)-4],
		"version":       mutate(func(c *Invitation) { c.Version = 2 }),
		"dns":           mutate(func(c *Invitation) { c.DNSName = "devbox.example.com" }),
		"dns case":      mutate(func(c *Invitation) { c.DNSName = "DevBox.example-tailnet.ts.net" }),
		"public ip":     mutate(func(c *Invitation) { c.IPs = []string{"8.8.8.8"} }),
		"no ip":         mutate(func(c *Invitation) { c.IPs = nil }),
		"duplicate ip":  mutate(func(c *Invitation) { c.IPs = []string{"100.64.0.10", "100.64.0.10"} }),
		"port":          mutate(func(c *Invitation) { c.Port = 0 }),
		"pin":           mutate(func(c *Invitation) { c.Pin = "AB" + c.Pin[2:] }),
		"name":          mutate(func(c *Invitation) { c.Name = "Dev Box" }),
		"short secret":  mutate(func(c *Invitation) { c.Secret = "abc" }),
		"unknown field": InvitationPrefix + base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"x":1}`)),
		"too long":      InvitationPrefix + strings.Repeat("A", maxInvitationBytes),
	}
	for name, text := range bad {
		if _, err := ParseInvitation(text); err == nil || !strings.HasPrefix(err.Error(), "link_invitation_invalid") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// One address family is enough.
	v6 := mutate(func(c *Invitation) { c.IPs = []string{"fd7a:115c:a1e0::a"} })
	if _, err := ParseInvitation(v6); err != nil {
		t.Fatalf("v6 only: %v", err)
	}
	if SecretHash(i.Secret) == SecretHash(i.Secret+"x") || len(SecretHash(i.Secret)) != 64 {
		t.Fatal("hash")
	}
}

func testPair(id, pin, name string) Pair {
	return Pair{ID: id, Advertised: name, Effective: name, Pin: pin, DNSName: "peer.example-tailnet.ts.net", IPs: []string{"100.64.0.20"}, Port: DefaultPort, State: Paired, Role: Joiner, Created: 1}
}

func TestStateRoundTripAndValidation(t *testing.T) {
	root := privateRoot(t)
	s, err := ReadState(root)
	if err != nil || s.Version != 1 || len(s.Pairs) != 0 {
		t.Fatalf("empty %v %+v", err, s)
	}
	s.Name, s.Port = "devbox", DefaultPort
	s.Pairs = []Pair{testPair(strings.Repeat("1", 32), strings.Repeat("a", 64), "laptop")}
	s.Invites = []Invite{{SecretHash: strings.Repeat("c", 64), Created: 1, Expires: 2}}
	if err := WriteState(root, s); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(root, StateFile))
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v", info.Mode())
	}
	got, err := ReadState(root)
	if err != nil || got.Name != "devbox" || len(got.Pairs) != 1 || got.Pairs[0].Effective != "laptop" || len(got.Invites) != 1 {
		t.Fatalf("read %v %+v", err, got)
	}
	bad := map[string]func(*State){
		"duplicate id":   func(s *State) { s.Pairs = append(s.Pairs, testPair(s.Pairs[0].ID, strings.Repeat("b", 64), "other")) },
		"duplicate pin":  func(s *State) { s.Pairs = append(s.Pairs, testPair(strings.Repeat("2", 32), s.Pairs[0].Pin, "other")) },
		"duplicate name": func(s *State) { s.Pairs = append(s.Pairs, testPair(strings.Repeat("2", 32), strings.Repeat("b", 64), "laptop")) },
		"pending no exp": func(s *State) { s.Pairs[0].State = Pending },
		"bad state":      func(s *State) { s.Pairs[0].State = "open" },
		"bad role":       func(s *State) { s.Pairs[0].Role = "x" },
		"bad ip":         func(s *State) { s.Pairs[0].IPs = []string{"10.0.0.1"} },
		"bad name":       func(s *State) { s.Name = "Dev" },
		"bad version":    func(s *State) { s.Version = 2 },
		"bad invite":     func(s *State) { s.Invites[0].Expires = 0 },
	}
	for name, f := range bad {
		c := got
		c.Pairs = append([]Pair(nil), got.Pairs...)
		c.Invites = append([]Invite(nil), got.Invites...)
		f(&c)
		if c.Validate() == nil || WriteState(root, c) == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := os.WriteFile(filepath.Join(root, StateFile), []byte(`{"version":1,"extra":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadState(root); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestFreeName(t *testing.T) {
	s := State{Version: 1, Pairs: []Pair{testPair(strings.Repeat("1", 32), strings.Repeat("a", 64), "devbox"), testPair(strings.Repeat("2", 32), strings.Repeat("b", 64), "devbox-2")}}
	if got := s.FreeName("devbox", ""); got != "devbox-3" {
		t.Fatalf("free %q", got)
	}
	if got := s.FreeName("devbox", strings.Repeat("1", 32)); got != "devbox" {
		t.Fatalf("rename keeps own %q", got)
	}
	if got := s.FreeName("laptop", ""); got != "laptop" {
		t.Fatalf("unused %q", got)
	}
	long := strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 61)
	s.Pairs[0].Effective = long
	got := s.FreeName(long, "")
	if ValidateName(got) != nil || got == long || !strings.HasSuffix(got, "-2") {
		t.Fatalf("long %q", got)
	}
}

func TestFrames(t *testing.T) {
	var buf bytes.Buffer
	f, _ := NewFrame("ping", map[string]int{"n": 1})
	if err := WriteFrame(&buf, f); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(&buf)
	var body struct{ N int }
	if err != nil || got.Op != "ping" || got.Decode(&body) != nil || body.N != 1 {
		t.Fatalf("%v %+v", err, got)
	}
	big, _ := NewFrame("x", strings.Repeat("a", MaxFrameBytes))
	if WriteFrame(&buf, big) == nil {
		t.Fatal("large frame written")
	}
	for _, raw := range [][]byte{{0, 0, 0, 0}, {0, 1, 0, 1}, append([]byte{0, 0, 0, 9}, []byte(`{"op":""}`)...), append([]byte{0, 0, 0, 15}, []byte(`{"op":"a","x":1}`)...)} {
		if _, err := ReadFrame(bytes.NewReader(raw)); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	var unknown struct{ A int }
	if (Frame{Op: "x", Body: []byte(`{"a":1,"b":2}`)}).Decode(&unknown) == nil {
		t.Fatal("unknown body field accepted")
	}
}
