package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const remoteSessionLifetime = 48 * time.Hour
const maxRemoteSessions = 32

// remoteObserveEvery is how often a remote login's last observation is written.
const remoteObserveEvery = time.Minute

var ErrRemoteCapacity = errors.New("remote_session_capacity: forget a signed-in device before signing in another browser")
var ErrRevocationIncomplete = errors.New("Revocation incomplete; private storage could not be updated")

// RemoteDevice describes a browser session, never physical device identity.
type RemoteDevice struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	CreatedAt    int64  `json:"created_at"`
	LastObserved int64  `json:"last_observed"`
	ExpiresAt    int64  `json:"expires_at"`
	ThisBrowser  bool   `json:"this_browser"`
}

// The random PBKDF2 salt changes on every secret replacement (including replacing
// it with the same text). Its digest binds sessions without copying a reusable key.
func (a *dashboardAuth) credentialGeneration() (string, error) {
	var salt []byte
	err := a.store.db.QueryRow(`SELECT salt FROM maintainer_secret WHERE id=1`).Scan(&salt)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte("remote-session-generation-v2:"), salt...))
	return hex.EncodeToString(digest[:]), nil
}

type durableRemoteSession struct {
	Version int `json:"version"`
	RemoteDevice
	Origin     string `json:"origin"`
	Generation string `json:"generation"`
	Hash       string `json:"token_hash"`
	Revoked    string `json:"revoked"`
}

const remoteSessionPrefix = "remote.v2.session:"

// readRemoteLocked returns the valid session rows. A row that fails its checks is left out,
// so its cookie is refused, and its name is kept in invalidRemote for diagnostics and for
// forget-all, which deletes it; one bad row never blocks the other logins (#673 review M3).
// A row's deadline is checked against its own creation time, not against the current
// lifetime constant, so a later change of the lifetime does not invalidate older rows; a
// row longer than the current lifetime is refused.
func (a *dashboardAuth) readRemoteLocked() ([]durableRemoteSession, error) {
	rows, err := a.store.db.Query(`SELECT name, value FROM settings WHERE name GLOB 'remote.v2.session:*'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []durableRemoteSession
	var invalid []string
	for rows.Next() {
		var name, value string
		var s durableRemoteSession
		if err = rows.Scan(&name, &value); err != nil {
			return nil, err
		}
		if !validRemoteRow(name, value, &s) {
			invalid = append(invalid, name)
			continue
		}
		sessions = append(sessions, s)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	a.invalidRemote = invalid
	return sessions, nil
}

// validRemoteRow decodes one stored session and checks its fields.
func validRemoteRow(name, value string, s *durableRemoteSession) bool {
	if json.Unmarshal([]byte(value), s) != nil {
		return false
	}
	_, idOK := tokenHash(s.ID)
	_, hashOK := tokenHash(s.Hash)
	_, generationOK := tokenHash(s.Generation)
	return s.Version == 2 && idOK && hashOK && generationOK && name == remoteSessionPrefix+s.ID &&
		s.ExpiresAt > s.CreatedAt && s.ExpiresAt-s.CreatedAt <= remoteSessionLifetime.Milliseconds() &&
		s.LastObserved >= s.CreatedAt
}

// deleteInvalidRemoteLocked removes the rows that readRemoteLocked left out.
func (a *dashboardAuth) deleteInvalidRemoteLocked() error {
	for _, name := range a.invalidRemote {
		tx, err := a.store.begin(context.Background(), control)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM settings WHERE name=?`, name); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	a.invalidRemote = nil
	return nil
}
func (a *dashboardAuth) saveRemoteLocked(s durableRemoteSession) error {
	return a.saveRemoteClassLocked(s, ordinary)
}
func (a *dashboardAuth) saveRemoteClassLocked(s durableRemoteSession, class writeClass) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	ctx := context.Background()
	tx, err := a.store.begin(ctx, class)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(name,value,updated_at) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, remoteSessionPrefix+s.ID, string(data), a.now().UnixMilli()); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

func (a *dashboardAuth) startRemoteLocked(hash [32]byte, origin string) error {
	now := a.now().UnixMilli()
	if a.store == nil { // isolated auth unit fixtures only; daemons always bind Store
		n := 0
		for _, s := range a.sessions {
			if strings.HasPrefix(s.client, "remote:") {
				n++
			}
		}
		if n >= maxRemoteSessions {
			return ErrRemoteCapacity
		}
		a.sessions[hash] = &dashboardSession{created: a.now(), used: a.now(), client: "remote:" + origin}
		return nil
	}
	generation, err := a.credentialGeneration()
	if err != nil {
		return err
	}
	sessions, err := a.readRemoteLocked()
	if err != nil {
		return err
	}
	count := 0
	for _, s := range sessions {
		if now > s.ExpiresAt+7*24*time.Hour.Milliseconds() {
			tx, err := a.store.begin(context.Background(), control)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(`DELETE FROM settings WHERE name=?`, remoteSessionPrefix+s.ID); err != nil {
				tx.Rollback()
				return err
			}
			if err = tx.Commit(); err != nil {
				return err
			}
			continue
		}
		if s.Revoked == "" && s.ExpiresAt > now && s.Generation == generation {
			count++
		}
	}
	if count >= maxRemoteSessions {
		return ErrRemoteCapacity
	}
	id, err := randomToken()
	if err != nil {
		return err
	}
	return a.saveRemoteLocked(durableRemoteSession{Version: 2, RemoteDevice: RemoteDevice{ID: id, Label: "Browser", CreatedAt: now, LastObserved: now, ExpiresAt: now + remoteSessionLifetime.Milliseconds()}, Origin: origin, Generation: generation, Hash: hex.EncodeToString(hash[:])})
}
func (a *dashboardAuth) remoteSessionLocked(hash [32]byte, origin string) bool {
	if a.denyAllRemote {
		a.authReason = "device_revoked"
		return false
	}
	sessions, err := a.readRemoteLocked()
	if err != nil {
		a.storeHealth = "unavailable"
		a.authReason = "unknown_credential"
		return false
	}
	current, err := a.credentialGeneration()
	if errors.Is(err, sql.ErrNoRows) {
		// The maintainer secret was cleared: every remote login has ended (#673 review L2).
		a.authReason = "password_changed"
		return false
	}
	if err != nil {
		a.storeHealth = "unavailable"
		return false
	}
	for _, s := range sessions {
		if s.Hash != hex.EncodeToString(hash[:]) {
			continue
		}
		if s.Origin != origin {
			a.authReason = "unknown_credential"
			return false
		}
		reason := s.Revoked
		if reason == "" {
			// Keep the first denial across a failed write and a clock rollback.
			// Do not let the in-memory deny list skip the durable retry.
			reason = a.denied[hash]
		}
		now := max(a.now().UnixMilli(), s.LastObserved)
		if reason == "" && s.Generation != current {
			reason = "password_changed"
		}
		if reason == "" && now >= s.ExpiresAt {
			reason = "session_expired"
		}
		if reason != "" {
			a.authReason = reason
			a.denied[hash] = reason
			if s.Revoked == "" {
				a.persistRemoteDenialLocked(s, reason)
			}
			return false
		}
		// The observation bounds a clock rollback; it is written at most once a minute, not
		// on every request of the phone's refresh (#673 review M4). A failed write still
		// denies, as storage failure does.
		if now-s.LastObserved >= remoteObserveEvery.Milliseconds() {
			s.LastObserved = now
			if a.saveRemoteClassLocked(s, control) != nil {
				a.storeHealth = "unavailable"
				return false
			}
		}
		a.storeHealth = "healthy"
		a.lastPhone = now
		return true
	}
	a.authReason = "unknown_credential"
	return false
}

// Persist with the original reason even if a repaired store is observed after
// the clock moved backward. Callers hold mu and continue to deny on failure.
func (a *dashboardAuth) persistRemoteDenialLocked(s durableRemoteSession, reason string) bool {
	s.Revoked = reason
	if a.pendingRevocations == nil {
		a.pendingRevocations = map[string]string{}
	}
	if a.saveRemoteClassLocked(s, control) != nil {
		a.pendingRevocations[s.ID] = reason
		a.storeHealth = "unavailable"
		return false
	}
	delete(a.pendingRevocations, s.ID)
	a.storeHealth = "healthy"
	return true
}

// revokeRemote is an explicit revocation. A revoke-all also cancels the open QR sign-in
// tickets; a single device's revoke and a diagnostics retry keep them (#673 review L1).
func (a *dashboardAuth) revokeRemote(id, reason string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id == "" {
		clear(a.tickets)
	}
	return a.revokeRemoteLocked(id, reason)
}
func (a *dashboardAuth) revokeRemoteLocked(id, reason string) (result error) {
	inventoryRead := false
	defer func() {
		if a.pendingRevocations == nil {
			a.pendingRevocations = map[string]string{}
		}
		if result != nil && !inventoryRead {
			a.pendingRevocations[id] = reason
		} else if result != nil {
			// Once the inventory is readable, retry only the failed rows, so
			// a later new sign-in is not included in an earlier forget-all.
			if id == "" || strings.HasPrefix(id, "hash:") {
				delete(a.pendingRevocations, id)
			}
		} else if id == "" {
			clear(a.pendingRevocations)
		} else {
			delete(a.pendingRevocations, id)
		}
		if result == nil && len(a.pendingRevocations) == 0 {
			a.denyAllRemote = false
		}
	}()
	for h, s := range a.sessions {
		if strings.HasPrefix(s.client, "remote:") {
			delete(a.sessions, h)
		}
	}
	if a.store == nil {
		return nil
	}
	sessions, err := a.readRemoteLocked()
	if err != nil {
		a.denyAllRemote = true
		a.storeHealth = "unavailable"
		return ErrRevocationIncomplete
	}
	inventoryRead = true
	failed := false
	for _, s := range sessions {
		if id != "" && s.ID != id && "hash:"+s.Hash != id {
			continue
		}
		hash, err := hex.DecodeString(s.Hash)
		if err != nil {
			a.denyAllRemote = true
			return ErrRevocationIncomplete
		}
		var h [32]byte
		copy(h[:], hash)
		// A later forget-all must not rewrite a previously recorded end reason.
		rowReason := s.Revoked
		if rowReason == "" {
			rowReason = a.denied[h]
		}
		if rowReason == "" {
			rowReason = reason
		}
		a.denied[h] = rowReason
		if s.Revoked == "" {
			s.Revoked = rowReason
			if a.saveRemoteClassLocked(s, control) != nil {
				if a.pendingRevocations == nil {
					a.pendingRevocations = map[string]string{}
				}
				a.pendingRevocations[s.ID] = rowReason
				failed = true
			} else {
				delete(a.pendingRevocations, s.ID)
			}
		} else {
			delete(a.pendingRevocations, s.ID)
		}
	}
	if id == "" && a.deleteInvalidRemoteLocked() != nil {
		failed = true
	}
	if failed {
		a.storeHealth = "unavailable"
		return ErrRevocationIncomplete
	}
	a.storeHealth = "healthy"
	if id == "" {
		a.denyAllRemote = false
	}
	return nil
}
func (a *dashboardAuth) sessionInfo(token string) RemoteDevice {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessionInfoLocked(token)
}
func (a *dashboardAuth) sessionInfoLocked(token string) RemoteDevice {
	hash, ok := tokenHash(token)
	if !ok || a.store == nil {
		return RemoteDevice{}
	}
	sessions, err := a.readRemoteLocked()
	if err != nil {
		return RemoteDevice{}
	}
	for _, s := range sessions {
		if s.Hash == hex.EncodeToString(hash[:]) {
			return s.RemoteDevice
		}
	}
	return RemoteDevice{}
}
func (a *dashboardAuth) remoteSessionID(token string) string { return a.sessionInfo(token).ID }
func (a *dashboardAuth) remoteDevices(token string) ([]RemoteDevice, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.store == nil {
		return nil, ErrUnavailable
	}
	generation, err := a.credentialGeneration()
	if errors.Is(err, sql.ErrNoRows) {
		return []RemoteDevice{}, nil
	}
	if err != nil {
		return nil, err
	}
	current := a.sessionInfoLocked(token).ID
	sessions, err := a.readRemoteLocked()
	if err != nil {
		return nil, err
	}
	devices := []RemoteDevice{}
	for _, s := range sessions {
		if s.Revoked == "" && s.Generation == generation && s.ExpiresAt > a.now().UnixMilli() {
			s.ThisBrowser = s.ID == current
			devices = append(devices, s.RemoteDevice)
		}
	}
	return devices, nil
}

func (d *Daemon) dashboardDeviceRoutes(mux *http.ServeMux, authed func(func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc) {
	mux.HandleFunc("GET /dashboard/remote/devices", authed(func(w http.ResponseWriter, r *http.Request, id string) {
		devices, err := d.auth.remoteDevices(id)
		if err != nil {
			http.Error(w, "Private session storage unavailable", 503)
			return
		}
		respond(w, 200, map[string]any{"devices": devices, "current": d.auth.sessionInfo(id), "csrf": d.auth.csrf(id)})
	}))
	mux.HandleFunc("POST /dashboard/remote/devices", authed(func(w http.ResponseWriter, r *http.Request, id string) {
		target := r.PostForm.Get("id")
		action := r.PostForm.Get("action")
		label := strings.TrimSpace(r.PostForm.Get("label"))
		var err error
		switch {
		case action == "revoke-all":
			err = d.auth.revokeRemote("", "device_revoked")
		case action != "revoke" && action != "rename", target == "",
			action == "rename" && (label == "" || len(label) > 80 || strings.ContainsAny(label, "\r\n\x00")):
			err = ErrInvalid
		default:
			err = d.auth.changeRemoteDevice(target, action, label)
		}
		// A wrong request is 400 and an unknown or ended device 404; only storage is 503
		// (#673 review L3).
		switch {
		case errors.Is(err, ErrInvalid):
			http.Error(w, "invalid request", http.StatusBadRequest)
		case errors.Is(err, ErrRemoteDeviceNotFound):
			http.Error(w, ErrRemoteDeviceNotFound.Error(), http.StatusNotFound)
		case err != nil:
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
		default:
			respond(w, 200, map[string]any{"ok": true})
		}
	}))
}

// ErrRemoteDeviceNotFound is a revoke or rename of a device that is not signed in.
var ErrRemoteDeviceNotFound = errors.New("remote_device_not_found: this device is not signed in; reload the list")

// changeRemoteDevice revokes or renames one signed-in remote device.
func (a *dashboardAuth) changeRemoteDevice(id, action, label string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	sessions, err := a.readRemoteLocked()
	if err != nil {
		return err
	}
	now := a.now().UnixMilli()
	for _, s := range sessions {
		if s.ID != id || s.Revoked != "" || s.ExpiresAt <= now {
			continue
		}
		if action == "revoke" {
			return a.revokeRemoteLocked(id, "device_revoked")
		}
		s.Label = label
		return a.saveRemoteLocked(s)
	}
	return ErrRemoteDeviceNotFound
}

// These observations describe host requests, not cellular connectivity or a
// person's current presence. Store health is unavailable until a successful read.
func (a *dashboardAuth) remoteDiagnostics() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := map[string]any{"store_health": a.storeHealth, "phone_connected_at": a.lastPhone, "last_auth_end_reason": a.authReason, "revocation_incomplete": len(a.pendingRevocations) > 0}
	if a.store == nil {
		result["store_health"] = "unavailable"
		return result
	}
	// Snapshot keys: retries mutate the queue. No browser cookie is required.
	pending := make(map[string]string, len(a.pendingRevocations))
	for id, reason := range a.pendingRevocations {
		pending[id] = reason
	}
	persistenceFailed := false
	for id, reason := range pending {
		if a.revokeRemoteLocked(id, reason) != nil {
			persistenceFailed = true
		}
	}
	sessions, err := a.readRemoteLocked()
	if err != nil {
		result["store_health"] = "unavailable"
		return result
	}
	generation, err := a.credentialGeneration()
	if errors.Is(err, sql.ErrNoRows) {
		result["secret_set"], result["active_sessions"], result["invalid_sessions"] = false, 0, len(a.invalidRemote)
		return result
	}
	if err != nil {
		result["store_health"] = "unavailable"
		return result
	}
	count := 0
	for _, s := range sessions {
		h, err := hex.DecodeString(s.Hash)
		if err != nil {
			result["store_health"] = "unavailable"
			return result
		}
		var hash [32]byte
		copy(hash[:], h)
		reason := a.denied[hash]
		if s.Revoked == "" && reason == "" && s.Generation == generation && s.ExpiresAt > a.now().UnixMilli() {
			count++
		}
	}
	if persistenceFailed {
		a.storeHealth = "unavailable"
	}
	result["store_health"] = a.storeHealth
	result["revocation_incomplete"] = len(a.pendingRevocations) > 0
	result["active_sessions"] = count
	result["invalid_sessions"] = len(a.invalidRemote)
	return result
}

// Re-read only the rejected credential's tombstone; never publish another
// request's latest authentication diagnostic on this response.
func (a *dashboardAuth) remoteRejectionReason(r *http.Request) string {
	cookie, err := r.Cookie(cookieName(r))
	if err != nil {
		return "unknown_credential"
	}
	hash, ok := tokenHash(cookie.Value)
	if !ok {
		return "unknown_credential"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if reason := a.denied[hash]; reason != "" {
		return reason
	}
	if a.store == nil {
		return "unknown_credential"
	}
	sessions, err := a.readRemoteLocked()
	if err != nil {
		return "unknown_credential"
	}
	for _, s := range sessions {
		if s.Hash == hex.EncodeToString(hash[:]) && s.Revoked != "" {
			return s.Revoked
		}
	}
	return "unknown_credential"
}

// Only trusted saved configuration drives origin revocation. A request carrying
// a copied cookie on a different listener must never revoke the original login.
func (a *dashboardAuth) reconcileRemoteOrigin(origin string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.store == nil {
		return nil
	}
	sessions, err := a.readRemoteLocked()
	if err != nil {
		a.denyAllRemote = true
		a.storeHealth = "unavailable"
		return ErrRevocationIncomplete
	}
	for _, s := range sessions {
		if s.Revoked == "" && s.Origin != origin {
			if err := a.revokeRemoteLocked(s.ID, "origin_changed"); err != nil {
				return err
			}
		}
	}
	if len(a.pendingRevocations) == 0 {
		a.denyAllRemote = false
	}
	return nil
}
