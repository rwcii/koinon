package core

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The maintainer secret (PDR 009, Decisions 1 and 2). The store keeps one PBKDF2-HMAC-SHA256
// key derived from it with a random salt, never the secret. A correct secret starts a
// dashboard login; the one-time link from koinon dashboard stays the host-local way in.

const (
	secretAlgorithm  = "pbkdf2-sha256"
	secretIterations = 600000
	secretSaltBytes  = 16
	secretKeyBytes   = 32
	// MaintainerSecretMinRunes and MaintainerSecretMaxBytes bound a secret that the
	// maintainer chooses; a generated one is 43 characters.
	MaintainerSecretMinRunes = 12
	MaintainerSecretMaxBytes = 1024
	// The fifth failed check in a row starts a wait of secretFirstWait; each later one
	// doubles it, up to secretMaxWait.
	secretFreeFailures = 4
	secretFirstWait    = time.Second
	secretMaxWait      = 60 * time.Second
)

var (
	ErrSecretUnset   = Refusal{"maintainer_secret_unset", "no maintainer secret is set; run koinon maintainer secret set or generate on the daemon's host"}
	ErrSecretInvalid = Refusal{"maintainer_secret_invalid", "the maintainer secret is not correct"}
	// ErrSecretLength refuses a secret outside the bounds of ValidMaintainerSecret.
	ErrSecretLength = Refusal{"invalid_request", "a maintainer secret is valid UTF-8 with at least 12 characters and at most 1024 bytes"}
)

// SecretBackoff refuses a check during the wait after repeated failures; RetryAfter is
// when the next check is allowed.
type SecretBackoff struct{ RetryAfter time.Time }

func (b SecretBackoff) Error() string { return "maintainer_secret_backoff" }

// ValidMaintainerSecret reports whether secret can be stored: valid UTF-8 of at least
// MaintainerSecretMinRunes characters and at most MaintainerSecretMaxBytes bytes.
func ValidMaintainerSecret(secret string) bool {
	return len(secret) <= MaintainerSecretMaxBytes && utf8.ValidString(secret) && utf8.RuneCountInString(secret) >= MaintainerSecretMinRunes
}

func deriveSecret(secret string, salt []byte, iterations int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, secret, salt, iterations, secretKeyBytes)
}

// GenerateMaintainerSecret returns 32 random bytes as base64url without padding.
func GenerateMaintainerSecret() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// iterations is the PBKDF2 cost of a new key; tests lower it.
func (s *Store) iterations() int {
	if s.secretCost > 0 {
		return s.secretCost
	}
	return secretIterations
}

// setMaintainerSecret stores the key of secret with a new salt, replacing any earlier key,
// under the change's audit record.
func (s *Store) setMaintainerSecret(ctx context.Context, secret, action string) error {
	if !ValidMaintainerSecret(secret) {
		return ErrSecretLength
	}
	salt := make([]byte, secretSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	iterations := s.iterations()
	key, err := deriveSecret(secret, salt, iterations)
	if err != nil {
		return err
	}
	ctx, pending := withAudit(ctx, "maintainer_secret", action)
	pending.origin = "cli"
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.audited = true
	if _, err := tx.ExecContext(ctx, `INSERT INTO maintainer_secret(id,algorithm,salt,iterations,derived_key,updated_at) VALUES (1,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET algorithm=excluded.algorithm,salt=excluded.salt,iterations=excluded.iterations,
		derived_key=excluded.derived_key,updated_at=excluded.updated_at`, secretAlgorithm, salt, iterations, key, s.now().UnixMilli()); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// clearMaintainerSecret removes the key; with none set it refuses.
func (s *Store) clearMaintainerSecret(ctx context.Context) error {
	ctx, pending := withAudit(ctx, "maintainer_secret", "clear")
	pending.origin = "cli"
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.audited = true
	result, err := tx.ExecContext(ctx, `DELETE FROM maintainer_secret`)
	if err != nil {
		return tx.fail(err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrSecretUnset
	}
	return tx.Commit()
}

// maintainerSecretSet reports whether a key is stored.
func (s *Store) maintainerSecretSet(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM maintainer_secret`).Scan(&n)
	return n > 0, err
}

// matchMaintainerSecret derives the key of secret with the stored salt and iterations and
// compares it in constant time.
func (s *Store) matchMaintainerSecret(ctx context.Context, secret string) (bool, error) {
	var salt, stored []byte
	var iterations int
	err := s.db.QueryRowContext(ctx, `SELECT salt,iterations,derived_key FROM maintainer_secret WHERE id=1`).Scan(&salt, &iterations, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrSecretUnset
	}
	if err != nil {
		return false, err
	}
	if len(secret) > MaintainerSecretMaxBytes {
		return false, nil
	}
	key, err := deriveSecret(secret, salt, iterations)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(key, stored) == 1, nil
}

// secretGate runs one secret check or change at a time, apart from the login lock, so
// session checks and the other APIs stay responsive while PBKDF2 runs. Because a change
// waits for a running check, a check against a replaced key never starts a login after
// the change ended every login.
type secretGate struct {
	mu       chanMutex
	failures int
	retry    time.Time
}

// chanMutex is a mutex whose holder a context can stop waiting for.
type chanMutex chan struct{}

func (m chanMutex) lock(ctx context.Context) error {
	select {
	case m <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m chanMutex) unlock() { <-m }

// wait is the pause after failures failed checks in a row: none for the first four, then
// secretFirstWait doubling to secretMaxWait.
func secretWait(failures int) time.Duration {
	n := failures - secretFreeFailures - 1
	if n < 0 {
		return 0
	}
	if n >= 6 {
		return secretMaxWait
	}
	return secretFirstWait << n
}

// passwordLogin checks secret and, when it matches, records the login and starts it. A
// check during the wait computes no key and leaves the wait as it is.
func (d *Daemon) passwordLogin(ctx context.Context, secret, client string) (string, error) {
	g := &d.auth.gate
	if err := g.mu.lock(ctx); err != nil {
		return "", err
	}
	defer g.mu.unlock()
	if now := d.auth.now(); now.Before(g.retry) {
		return "", SecretBackoff{g.retry}
	}
	ok, err := d.store.matchMaintainerSecret(ctx, secret)
	if err != nil {
		return "", err
	}
	if !ok {
		g.failures++
		if wait := secretWait(g.failures); wait > 0 {
			g.retry = d.auth.now().Add(wait)
		}
		_, pending := withAudit(ctx, "maintainer_login", client)
		d.store.auditRefusal(ctx, pending, "secret_invalid")
		return "", ErrSecretInvalid
	}
	g.failures, g.retry = 0, time.Time{}
	if err := d.store.auditAccepted(ctx, "maintainer_login", client); err != nil {
		return "", err
	}
	return d.auth.start()
}

// changeSecret sets, generates or clears the secret and then ends every dashboard login.
// It returns a generated secret.
func (d *Daemon) changeSecret(ctx context.Context, action, secret string) (string, error) {
	g := &d.auth.gate
	if err := g.mu.lock(ctx); err != nil {
		return "", err
	}
	defer g.mu.unlock()
	var err error
	switch action {
	case "set":
		err = d.store.setMaintainerSecret(ctx, secret, "set")
	case "generate":
		if secret != "" {
			return "", ErrInvalid
		}
		if secret, err = GenerateMaintainerSecret(); err == nil {
			err = d.store.setMaintainerSecret(ctx, secret, "generate")
		}
	case "clear":
		if secret != "" {
			return "", ErrInvalid
		}
		err = d.store.clearMaintainerSecret(ctx)
	default:
		return "", ErrInvalid
	}
	if err != nil {
		return "", err
	}
	d.auth.endAll()
	if action == "generate" {
		return secret, nil
	}
	return "", nil
}

func (d *Daemon) maintainerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/maintainer/secret", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Action string `json:"action"`
			Secret string `json:"secret"`
		}
		// JSON escaping can grow a secret of MaintainerSecretMaxBytes to six times its size.
		if err := decodeLimit(w, r, &request, 6*MaintainerSecretMaxBytes+256); err != nil {
			failure(w, err)
			return
		}
		generated, err := d.changeSecret(r.Context(), request.Action, request.Secret)
		if err != nil {
			failure(w, err)
			return
		}
		reply := map[string]any{"ok": true, "action": request.Action, "logins_ended": true}
		if generated != "" {
			reply["secret"] = generated
		}
		respond(w, 200, reply)
	})
}

// retryAfter is the Retry-After value of a wait, in whole seconds, at least 1.
func retryAfter(b SecretBackoff, now time.Time) string {
	return strconv.FormatInt(max(1, int64((b.RetryAfter.Sub(now)+time.Second-1)/time.Second)), 10)
}

// ErrNoSecret refuses an input that ends before any secret.
var ErrNoSecret = Refusal{"invalid_request", "no maintainer secret was given"}

// ReadSecretLine reads the first line of r one byte at a time, so it takes nothing after
// the line and never holds more than the longest secret. It removes one LF or CRLF; a line
// that ends at EOF counts. A longer line is refused, not cut.
func ReadSecretLine(r io.Reader) (string, error) {
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if n == 1 {
			if b[0] == '\n' {
				break
			}
			// One byte beyond the bound is room for the CR of a CRLF.
			if len(line) > MaintainerSecretMaxBytes {
				return "", ErrSecretLength
			}
			line = append(line, b[0])
			continue
		}
		if errors.Is(err, io.EOF) {
			if len(line) == 0 {
				return "", ErrNoSecret
			}
			if len(line) > MaintainerSecretMaxBytes {
				return "", ErrSecretLength
			}
			return string(line), nil
		}
		if err != nil {
			return "", err
		}
	}
	text := strings.TrimSuffix(string(line), "\r")
	if len(text) > MaintainerSecretMaxBytes {
		return "", ErrSecretLength
	}
	return text, nil
}
