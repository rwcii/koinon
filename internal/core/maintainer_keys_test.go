package core

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/signed"
)

// enrollment signs an enroll statement for pin with the seeded key at created.
func enrollment(t *testing.T, seed byte, pin string, created int64) (signed.Enrollment, ed25519.PrivateKey) {
	t.Helper()
	private := ed25519.NewKeyFromSeed([]byte(strings.Repeat(string(rune(seed)), 32)))
	public := private.Public().(ed25519.PublicKey)
	data, err := signed.Sign(map[string]any{"v": signed.Version, "key": signed.KeyID(public), "target": pin, "op": "enroll",
		"args": map[string]any{"user": "maintainer", "client": "voice", "public_key": base64.StdEncoding.EncodeToString(public)}, "created": created}, private)
	if err != nil {
		t.Fatal(err)
	}
	e, err := signed.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	en, err := signed.CheckEnrollment(e, pin, created)
	if err != nil {
		t.Fatal(err)
	}
	return en, private
}

// A statement adds a key once on this computer, also after a restart and after the key's
// removal; a new statement for the same key replaces its authorization (#693, D1.6-D1.7).
func TestMaintainerKeysEnrollOnce(t *testing.T) {
	s, root := testStore(t)
	ctx := context.Background()
	pin := strings.Repeat("ab", 32)
	first, _ := enrollment(t, 1, pin, 1000)
	added, err := s.AddMaintainerKey(ctx, first, "secret")
	if err != nil || added.Authorization != first.Authorization || added.User != "maintainer" {
		t.Fatalf("add: %+v %v", added, err)
	}
	if _, err := s.AddMaintainerKey(ctx, first, "secret"); !errors.Is(err, ErrStatementUsed) {
		t.Fatalf("reuse: %v", err)
	}
	if _, err := s.RemoveMaintainerKey(ctx, first.KeyID); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	s, err = openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	if _, err := s.AddMaintainerKey(ctx, first, "secret"); !errors.Is(err, ErrStatementUsed) {
		t.Fatalf("reuse after removal and restart: %v", err)
	}
	if _, err := s.RemoveMaintainerKey(ctx, first.KeyID); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("second removal: %v", err)
	}
	second, _ := enrollment(t, 1, pin, 1001)
	if _, err := s.AddMaintainerKey(ctx, second, "secret"); err != nil {
		t.Fatal(err)
	}
	third, _ := enrollment(t, 1, pin, 1002)
	if _, err := s.AddMaintainerKey(ctx, third, "dashboard"); err != nil {
		t.Fatal(err)
	}
	current, err := currentAuthorization(ctx, s.db, first.KeyID)
	if err != nil || current.Authorization != third.Authorization || current.AddedBy != "dashboard" || len(current.public) != ed25519.PublicKeySize {
		t.Fatalf("current: %+v %v", current, err)
	}
	all, _ := s.MaintainerKeys(ctx, true)
	now, _ := s.MaintainerKeys(ctx, false)
	if len(all) != 3 || len(now) != 1 || now[0].Authorization != third.Authorization {
		t.Fatalf("lists: %d %d", len(all), len(now))
	}
}
