package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Work notifications are ordinary writes after committed work transitions. The event
// stream and retained items reconstruct audiences if enqueueing could not commit.
const maxWorkAudiences = 256

type workAudience struct {
	Kind     string `json:"kind"`
	Family   string `json:"family,omitempty"`
	Role     string `json:"role,omitempty"`
	Consumer string `json:"consumer,omitempty"`
	Native   Key    `json:"native,omitempty"`
}

func (a workAudience) encoding() string { b, _ := json.Marshal(a); return string(b) }
func (a workAudience) key() string {
	sum := sha256.Sum256([]byte(a.encoding()))
	return hex.EncodeToString(sum[:])
}

type workAudienceHead struct {
	audience workAudience
	head     int64
	// Creator identities are recorded transport provenance, not consumer parsing.
	creators map[Key]bool
	human    bool
}

// Only routing fields are decoded. Scope, evidence and progress bodies are not needed
// to reconcile a notice, and claim/progress events do not notify their own worker.
type workAudienceEvent struct {
	WorkID           string    `json:"work_id"`
	ProposedRole     *WorkRole `json:"proposed_role"`
	ProposedAssignee *string   `json:"proposed_assignee"`
	CreatedConsumer  string    `json:"created_consumer"`
	ReportSeq        *int64    `json:"report_seq"`
	Report           *struct {
		ParticipantRole *WorkRole `json:"participant_role"`
	} `json:"report"`
}

func assignmentAudience(w workAudienceEvent) *workAudience {
	if w.ProposedRole != nil {
		return &workAudience{Kind: "role", Family: w.ProposedRole.Family, Role: w.ProposedRole.Role}
	}
	if w.ProposedAssignee != nil {
		return &workAudience{Kind: "consumer", Consumer: *w.ProposedAssignee}
	}
	return nil
}

func recordedNativeActor(actor string) Key {
	family, id, ok := strings.Cut(actor, ":")
	if ok && validKey(family, id) {
		return Key{family, id}
	}
	return Key{}
}

// audienceHeads reads only retained work events. Previous assignments remain relevant
// until their item's history expires, so an acknowledged removal is never recreated
// after row reclamation or a restart.
func (s *Store) audienceHeads(ctx context.Context, store string, now float64) (map[string]*workAudienceHead, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.seq,e.kind,e.payload,e.actor,COALESCE(m.writer_family,'')
		FROM work_events e JOIN work_items w ON w.store=e.store AND w.work_id=e.work_id
		LEFT JOIN memory_stores ms ON ms.store_id=e.store
		LEFT JOIN memory_entries m ON m.repository=ms.repository AND m.seq=e.seq
		WHERE e.store=? AND (w.expires_at IS NULL OR w.expires_at>?)
		AND e.kind IN ('created','proposed','edited','reported','accepted','rejected','lease-expired','progress-overdue')
		ORDER BY e.seq`, store, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	heads := map[string]*workAudienceHead{}
	add := func(a *workAudience, head int64) *workAudienceHead {
		if a == nil {
			return nil
		}
		key := a.key()
		if heads[key] == nil {
			heads[key] = &workAudienceHead{audience: *a, creators: map[Key]bool{}}
		}
		h := heads[key]
		h.head = max(h.head, head)
		return h
	}
	previous := map[string]*workAudience{}
	creators := map[string]Key{}
	humanCreators := map[string]bool{}
	reports := map[int64]*workAudience{}
	for rows.Next() {
		var seq int64
		var kind, payload, actor, family string
		if err := rows.Scan(&seq, &kind, &payload, &actor, &family); err != nil {
			return nil, err
		}
		var v workAudienceEvent
		if err := json.Unmarshal([]byte(payload), &v); err != nil {
			return nil, err
		}
		assigned := assignmentAudience(v)
		// A report concerns its creator; the worker's own claim/progress/result
		// activity is not another assignment notice and cannot create a wake loop.
		if kind != "reported" {
			add(assigned, seq)
		}
		if old := previous[v.WorkID]; kind == "proposed" && old != nil && (assigned == nil || old.key() != assigned.key()) {
			add(old, seq)
		}
		previous[v.WorkID] = assigned
		if kind == "created" {
			creators[v.WorkID], humanCreators[v.WorkID] = recordedNativeActor(actor), family == maintainerKey.Family
		}
		if kind == "reported" {
			a := &workAudience{Kind: "native", Native: recordedNativeActor(actor)}
			if v.Report != nil && v.Report.ParticipantRole != nil {
				a = &workAudience{Kind: "role", Family: v.Report.ParticipantRole.Family, Role: v.Report.ParticipantRole.Role}
			}
			reports[seq] = a
			h := add(&workAudience{Kind: "consumer", Consumer: v.CreatedConsumer}, seq)
			if k := creators[v.WorkID]; validKey(k.Family, k.ID) {
				h.creators[k] = true
			}
			h.human = h.human || humanCreators[v.WorkID]
		}
		if (kind == "accepted" || kind == "rejected") && v.ReportSeq != nil {
			add(reports[*v.ReportSeq], seq)
		}
	}
	return heads, rows.Err()
}

func (s *Store) audienceRecipient(ctx context.Context, tx *sql.Tx, repo string, h *workAudienceHead) (Key, string, error) {
	a := h.audience
	now := s.now().UnixMilli()
	participant := func(address string) (Key, string, error) {
		var family, holder, role string
		err := tx.QueryRowContext(ctx, `SELECT family,holder_id,role FROM names WHERE kind='alias' AND name=? AND repository=?`, address, repo).Scan(&family, &holder, &role)
		if errors.Is(err, sql.ErrNoRows) {
			return Key{}, "unresolved_participant", nil
		}
		if err != nil {
			return Key{}, "", err
		}
		held, err := holds(ctx, tx, now, family, holder, repo, role)
		if err != nil {
			return Key{}, "", err
		}
		if !held {
			return Key{}, "unheld_participant", nil
		}
		return Key{family, participantKey(address)}, "", nil
	}
	native := func(k Key) (Key, string, error) {
		if !validKey(k.Family, k.ID) {
			return Key{}, "unresolved_actor", nil
		}
		var eligible bool
		err := tx.QueryRowContext(ctx, `SELECT repository=? AND retired_at=0 AND expires_at>? FROM sessions WHERE family=? AND id=?`, repo, now, k.Family, k.ID).Scan(&eligible)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !eligible {
			return Key{}, "unresolved_actor", nil
		}
		return k, "", err
	}
	switch a.Kind {
	case "role":
		var address string
		err := tx.QueryRowContext(ctx, `SELECT name FROM names WHERE kind='alias' AND family=? AND role=? AND repository=?`, a.Family, a.Role, repo).Scan(&address)
		if errors.Is(err, sql.ErrNoRows) {
			return Key{}, "unheld_role", nil
		}
		if err != nil {
			return Key{}, "", err
		}
		return participant(address)
	case "native":
		return native(a.Native)
	case "consumer":
		if isParticipantID(a.Consumer) {
			return participant(a.Consumer[len(participantPrefix):])
		}
		if a.Consumer == maintainerConsumer && h.human {
			return maintainerKey, "", nil
		}
		var actor string
		err := tx.QueryRowContext(ctx, `SELECT actor FROM memory_cursors WHERE repository=? AND consumer=?`, repo, a.Consumer).Scan(&actor)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Key{}, "", err
		}
		if err == nil && actor != "" {
			if k, reason, err := native(recordedNativeActor(actor)); err != nil || reason == "" {
				return k, reason, err
			}
		}
		// Compare registry-derived native defaults; never split a claimed consumer.
		var family, id string
		err = tx.QueryRowContext(ctx, `SELECT family,id FROM sessions WHERE repository=? AND family||':'||id=? AND retired_at=0 AND expires_at>?`, repo, a.Consumer, now).Scan(&family, &id)
		if err == nil {
			return native(Key{family, id})
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Key{}, "", err
		}
		var eligible []Key
		for k := range h.creators {
			if k, reason, err := native(k); err != nil {
				return Key{}, "", err
			} else if reason == "" {
				eligible = append(eligible, k)
			}
		}
		if len(eligible) == 1 {
			return eligible[0], "", nil
		}
		return Key{}, "unresolved_consumer", nil
	}
	return Key{}, "unresolved_audience", nil
}

// ReconcileWorkNotices reads routing history outside the memory operation lock. Each
// audience update/message is its own serialized ordinary transaction. A refusal leaves
// the durable work event intact for the next reconciliation.
func (s *Store) ReconcileWorkNotices(ctx context.Context) error {
	s.work.noticeMu.Lock()
	defer s.work.noticeMu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT repository,store_id,head FROM memory_stores WHERE EXISTS(SELECT 1 FROM work_items WHERE store=store_id) OR EXISTS(SELECT 1 FROM work_audiences WHERE store=store_id) ORDER BY repository`)
	if err != nil {
		return err
	}
	type target struct {
		repo, store string
		head        int64
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.repo, &t.store, &t.head); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	var faults []error
	for _, t := range targets {
		heads, err := s.audienceHeads(ctx, t.store, s.clock())
		if err != nil {
			faults = append(faults, err)
			continue
		}
		if err := s.cleanWorkAudiences(ctx, t.repo, t.store, t.head, heads); err != nil {
			faults = append(faults, err)
		}
		keys := make([]string, 0, len(heads))
		for key := range heads {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := s.reconcileAudience(ctx, t.repo, t.store, key, heads[key]); err != nil {
				faults = append(faults, err)
			}
		}
	}
	return errors.Join(faults...)
}

func (s *Store) cleanWorkAudiences(ctx context.Context, repo, store string, observedHead int64, heads map[string]*workAudienceHead) error {
	rows, err := s.db.QueryContext(ctx, `SELECT audience,message_id FROM work_audiences WHERE store=? AND (message_id IS NULL OR NOT EXISTS
		(SELECT 1 FROM messages m JOIN sessions r ON r.family=m.recipient_family AND r.id=m.recipient_id WHERE m.id=message_id AND m.seq>r.acked_through))`, store)
	if err != nil {
		return err
	}
	var unused []string
	for rows.Next() {
		var key string
		var message sql.NullInt64
		if err := rows.Scan(&key, &message); err != nil {
			rows.Close()
			return err
		}
		if heads[key] == nil {
			unused = append(unused, key)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, key := range unused {
		s.wake.mu.Lock()
		s.memory.op.Lock()
		err = s.workWrite(ctx, repo, ordinary, func(tx *writeTx, _ string) error {
			// Every new work transition advances the store head. If history changed
			// since the unlocked scan, keep the row and retry on the next pass.
			// This checks one scalar instead of rebuilding all payloads under lock.
			var currentHead int64
			if err := tx.QueryRowContext(ctx, `SELECT head FROM memory_stores WHERE store_id=?`, store).Scan(&currentHead); err != nil {
				return err
			}
			if currentHead != observedHead {
				return nil
			}
			_, err := tx.ExecContext(ctx, `DELETE FROM work_audiences WHERE store=? AND audience=? AND (message_id IS NULL OR NOT EXISTS
				(SELECT 1 FROM messages m JOIN sessions r ON r.family=m.recipient_family AND r.id=m.recipient_id WHERE m.id=message_id AND m.seq>r.acked_through))`, store, key)
			return err
		})
		s.memory.op.Unlock()
		s.wake.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) reconcileAudience(ctx context.Context, repo, store, key string, h *workAudienceHead) error {
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	s.memory.op.Lock()
	defer s.memory.op.Unlock()
	// Leave unchanged pending watermarks alone rather than rewriting every audience
	// each sweep. Acknowledgement and holder changes share the lock above.
	var storedHead, storedNotice int64
	var storedMessage sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT head,notice_head,message_id FROM work_audiences WHERE store=? AND audience=?`, store, key).Scan(&storedHead, &storedNotice, &storedMessage)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && storedHead >= h.head {
		if storedMessage.Valid {
			var pending bool
			err := s.db.QueryRowContext(ctx, `SELECT m.seq>r.acked_through FROM messages m JOIN sessions r ON r.family=m.recipient_family AND r.id=m.recipient_id WHERE m.id=?`, storedMessage.Int64).Scan(&pending)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if pending {
				return nil
			}
		} else if storedHead <= storedNotice {
			return nil
		}
	}
	return s.workWrite(ctx, repo, ordinary, func(tx *writeTx, _ string) error {
		var head, noticeHead int64
		var message sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT head,notice_head,message_id FROM work_audiences WHERE store=? AND audience=?`, store, key).Scan(&head, &noticeHead, &message)
		if errors.Is(err, sql.ErrNoRows) {
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_audiences WHERE store=?`, store).Scan(&count); err != nil {
				return err
			}
			if count >= maxWorkAudiences {
				return nil
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO work_audiences(store,audience,data,head) VALUES(?,?,?,?)`, store, key, h.audience.encoding(), h.head); err != nil {
				return err
			}
			head = h.head
		} else if err != nil {
			return err
		}
		head = max(head, h.head)
		pending := false
		if message.Valid {
			err := tx.QueryRowContext(ctx, `SELECT m.seq>r.acked_through FROM messages m JOIN sessions r ON r.family=m.recipient_family AND r.id=m.recipient_id WHERE m.id=?`, message.Int64).Scan(&pending)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if pending {
			_, err := tx.ExecContext(ctx, `UPDATE work_audiences SET head=? WHERE store=? AND audience=?`, head, store, key)
			return err
		}
		if head <= noticeHead {
			_, err := tx.ExecContext(ctx, `UPDATE work_audiences SET message_id=NULL,reason='' WHERE store=? AND audience=?`, store, key)
			return err
		}
		recipient, reason, err := s.audienceRecipient(ctx, tx.Tx, repo, h)
		if err != nil {
			return err
		}
		if reason != "" {
			_, err := tx.ExecContext(ctx, `UPDATE work_audiences SET head=?,message_id=NULL,reason=? WHERE store=? AND audience=?`, head, reason, store, key)
			return err
		}
		if isParticipantID(recipient.ID) {
			if err := ensureParticipantInbox(ctx, tx.Tx, s.now().UnixMilli(), recipient.ID[len(participantPrefix):]); err != nil {
				return err
			}
		}
		var seq int64
		if err := tx.QueryRowContext(ctx, `UPDATE sessions SET last_seq=last_seq+1 WHERE family=? AND id=? RETURNING last_seq`, recipient.Family, recipient.ID).Scan(&seq); err != nil {
			return err
		}
		repositoryJSON, _ := json.Marshal(repo)
		body := fmt.Sprintf("Koinon work in %s has changes through head %d. Read current work queries and memory_sync through that head.", repositoryJSON, head)
		now := s.now().UnixMilli()
		result, err := tx.ExecContext(ctx, `INSERT INTO messages(recipient_family,recipient_id,seq,sender_family,sender_id,sender_name,body,created_at,delivery_updated_at) VALUES(?,?,?,'daemon','work','koinon-daemon',?,?,?)`, recipient.Family, recipient.ID, seq, body, now, now)
		if err != nil {
			return err
		}
		id, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if err := bumpInbox(ctx, tx, recipient); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE work_audiences SET head=?,notice_head=?,message_id=?,reason='' WHERE store=? AND audience=?`, head, head, id, store, key)
		return err
	})
}

func (s *Store) workNoticeStatus(ctx context.Context, repo string) (map[string]any, error) {
	store, err := storeOf(ctx, s.db, repo)
	if err != nil {
		return nil, err
	}
	heads, err := s.audienceHeads(ctx, store, s.clock())
	if err != nil {
		return nil, err
	}
	var count, pending, unresolved int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(message_id IS NOT NULL),0),COALESCE(SUM(reason<>''),0) FROM work_audiences WHERE store=?`, store).Scan(&count, &pending, &unresolved); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT audience FROM work_audiences WHERE store=?`, store)
	if err != nil {
		return nil, err
	}
	stored := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		stored[key] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var missing int64
	for key := range heads {
		if !stored[key] {
			missing++
		}
	}
	return map[string]any{"audiences": count, "limit": maxWorkAudiences, "pending": pending, "unresolved": unresolved, "missing_audiences": missing, "saturated": count >= maxWorkAudiences && missing > 0, "observed_at": s.clock()}, nil
}

func (d *Daemon) signalWorkNotices() {
	select {
	case d.workNoticeSignal <- struct{}{}:
	default:
	}
}

func (d *Daemon) maintainWorkNotices() {
	defer d.wg.Done()
	for {
		ctx, cancel := contextUntil(d.stop, time.Minute)
		d.store.ReconcileWorkNotices(ctx)
		cancel()
		select {
		case <-d.stop:
			return
		case <-d.workNoticeSignal:
		case <-time.After(workSweepInterval):
		}
	}
}
