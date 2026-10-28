package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Decision items (#206, chunk 01): agent-written records on the maintainer's decision
// board. Agents query them in bounded pages; no agent reads the dashboard HTML.

const (
	decisionRefLimit            = 32
	decisionTitleLimit          = 256
	decisionWhereLimit          = 256
	decisionAgentTextLimit      = 8 << 10
	decisionContextLimit        = 16 << 10
	decisionRecommendationLimit = 8 << 10
	decisionCommandLimit        = 512
	decisionCommandCount        = 8
	decisionRecipientLimit      = 256
	decisionRecipientCount      = 32
	decisionKeyLimit            = 256
	decisionPageItems           = 100
	decisionPageBytes           = 64 << 10
	// A create or edit at every field limit, with every byte escaped in JSON.
	decisionRequestBytes = 6*(decisionRefLimit+decisionTitleLimit+decisionWhereLimit+decisionAgentTextLimit+
		decisionContextLimit+decisionRecommendationLimit+decisionCommandCount*decisionCommandLimit+
		decisionRecipientCount*decisionRecipientLimit+decisionKeyLimit) + 8192
)

// Version 25: decision items and their recipients (#206, chunk 01). Every list reads one
// index range in item ID order: (state, kind, id) for waiting, (state, id) for a state,
// and (name, item_id) for the caller's own items. Peer names and addresses share one
// namespace, so a recipient name names the caller exactly when it is the caller's peer name
// or held address now, also when it did not resolve at the write. (state, archived_at, id) serves
// the deep-archive sweep (chunk 02).
const decisionTables = decisionItemsTableV25 + `;
	CREATE INDEX decision_items_waiting ON decision_items(state, kind, id);
	CREATE INDEX decision_items_state ON decision_items(state, id);
	CREATE INDEX decision_items_archive ON decision_items(state, archived_at, id);
	CREATE TABLE decision_recipients (
		item_id INTEGER NOT NULL, ordinal INTEGER NOT NULL, name TEXT NOT NULL,
		kind TEXT NOT NULL CHECK (kind IN ('participant','peer','')), reason TEXT NOT NULL,
		PRIMARY KEY (item_id, ordinal));
	CREATE INDEX decision_recipients_name ON decision_recipients(name, item_id);
	CREATE TABLE decision_replays (
		family TEXT NOT NULL, caller_id TEXT NOT NULL, key TEXT NOT NULL,
		fingerprint TEXT NOT NULL, deadline REAL NOT NULL, result TEXT NOT NULL,
		PRIMARY KEY (family, caller_id, key));`

// decisionItemsTableV25 is the item table of schema 25, which later versions extend.
const decisionItemsTableV25 = `
	CREATE TABLE decision_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT, reference TEXT NOT NULL UNIQUE,
		item_order INTEGER NOT NULL, title TEXT NOT NULL, location TEXT NOT NULL,
		agent_text TEXT NOT NULL, context TEXT NOT NULL, recommendation TEXT NOT NULL,
		kind TEXT NOT NULL CHECK (kind IN ('decision','fyi')),
		state TEXT NOT NULL CHECK (state IN ('active','in_progress','archived')),
		commands TEXT NOT NULL, revision INTEGER NOT NULL CHECK (revision>0),
		author_family TEXT NOT NULL, author_name TEXT NOT NULL,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, archived_at INTEGER NOT NULL DEFAULT 0)`

// decisionWithdrawalsTable (schema 33, #504) keeps each agent withdrawal of an active item:
// the revision, the withdrawing peer, its reason and time, and the reopen that undid it.
const decisionWithdrawalsTable = `CREATE TABLE decision_withdrawals (
	id INTEGER PRIMARY KEY AUTOINCREMENT, item_id INTEGER NOT NULL, revision INTEGER NOT NULL,
	family TEXT NOT NULL, name TEXT NOT NULL, reason TEXT NOT NULL, at INTEGER NOT NULL,
	reopened_at INTEGER NOT NULL DEFAULT 0, reopened_by TEXT NOT NULL DEFAULT '');
	CREATE INDEX decision_withdrawals_item ON decision_withdrawals(item_id, id);`

// decisionBoardTablesV36 (schema 36, #537) rebuild the item and verdict tables under their own
// names, because SQLite cannot widen a CHECK in place: an item can be a task and can be
// returned to its author, and a verdict can be feedback, completed or acknowledged. Every
// row is copied unchanged with its ID, each table keeps its AUTOINCREMENT high-water mark,
// so a deep-archived item's ID is never reused, and open fyi items whose title starts with
// ACTION FOR YOU become tasks.
const decisionBoardTablesV36 = `
	ALTER TABLE decision_items RENAME TO decision_items_v35;
	DROP INDEX decision_items_waiting; DROP INDEX decision_items_state; DROP INDEX decision_items_archive;
	CREATE TABLE decision_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT, reference TEXT NOT NULL UNIQUE,
		item_order INTEGER NOT NULL, title TEXT NOT NULL, location TEXT NOT NULL,
		agent_text TEXT NOT NULL, context TEXT NOT NULL, recommendation TEXT NOT NULL,
		kind TEXT NOT NULL CHECK (kind IN ('decision','task','fyi')),
		state TEXT NOT NULL CHECK (state IN ('active','returned','in_progress','archived')),
		commands TEXT NOT NULL, revision INTEGER NOT NULL CHECK (revision>0),
		author_family TEXT NOT NULL, author_name TEXT NOT NULL,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, archived_at INTEGER NOT NULL DEFAULT 0,
		closed_by TEXT NOT NULL DEFAULT '', closed_at INTEGER NOT NULL DEFAULT 0, outcome_note TEXT NOT NULL DEFAULT '',
		mark_revision INTEGER NOT NULL DEFAULT 0, mark_file TEXT NOT NULL DEFAULT '', mark_offset INTEGER NOT NULL DEFAULT 0,
		verdict_count INTEGER NOT NULL DEFAULT 0, author_participant TEXT NOT NULL DEFAULT '');
	INSERT INTO decision_items(` + decisionItemColumns + `) SELECT ` + decisionItemColumns + ` FROM decision_items_v35;
	UPDATE sqlite_sequence SET seq=(SELECT MAX(seq) FROM sqlite_sequence WHERE name IN ('decision_items','decision_items_v35')) WHERE name='decision_items';
	INSERT INTO sqlite_sequence(name,seq) SELECT 'decision_items',seq FROM sqlite_sequence WHERE name='decision_items_v35'
		AND NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name='decision_items');
	DROP TABLE decision_items_v35;
	CREATE INDEX decision_items_waiting ON decision_items(state, kind, id);
	CREATE INDEX decision_items_state ON decision_items(state, id);
	CREATE INDEX decision_items_archive ON decision_items(state, archived_at, id);
	UPDATE decision_items SET kind='task' WHERE kind='fyi' AND state='active' AND substr(title,1,14)='ACTION FOR YOU';
	ALTER TABLE decision_verdicts RENAME TO decision_verdicts_v35;
	DROP INDEX decision_verdicts_item;
	CREATE TABLE decision_verdicts (
		id INTEGER PRIMARY KEY AUTOINCREMENT, item_id INTEGER NOT NULL, revision INTEGER NOT NULL,
		verdict TEXT NOT NULL CHECK (verdict IN ('approved','declined','feedback','completed','acknowledged','dismissed')),
		note TEXT NOT NULL, created_at INTEGER NOT NULL, snapshot TEXT NOT NULL,
		superseded_at INTEGER NOT NULL DEFAULT 0, via TEXT NOT NULL DEFAULT '');
	INSERT INTO decision_verdicts(id,item_id,revision,verdict,note,created_at,snapshot,superseded_at,via)
		SELECT id,item_id,revision,verdict,note,created_at,snapshot,superseded_at,via FROM decision_verdicts_v35;
	UPDATE sqlite_sequence SET seq=(SELECT MAX(seq) FROM sqlite_sequence WHERE name IN ('decision_verdicts','decision_verdicts_v35')) WHERE name='decision_verdicts';
	INSERT INTO sqlite_sequence(name,seq) SELECT 'decision_verdicts',seq FROM sqlite_sequence WHERE name='decision_verdicts_v35'
		AND NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name='decision_verdicts');
	DROP TABLE decision_verdicts_v35;
	CREATE INDEX decision_verdicts_item ON decision_verdicts(item_id, id);`

// decisionItemColumns are the item columns that the schema 36 rebuild copies.
const decisionItemColumns = `id,reference,item_order,title,location,agent_text,context,recommendation,kind,state,commands,revision,
	author_family,author_name,created_at,updated_at,archived_at,closed_by,closed_at,outcome_note,mark_revision,mark_file,mark_offset,
	verdict_count,author_participant`

var decisionReference = regexp.MustCompile(`^[A-Za-z0-9-]{1,32}$`)

// credentialFormats are the known credential formats refused in every decision text
// field. The list is fixed here and in PROTOCOL; the daemon cannot find all personal data.
var credentialFormats = []struct {
	name string
	re   *regexp.Regexp
}{
	{"private key block", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)},
	{"GitHub token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}|\bgithub_pat_[A-Za-z0-9_]{22,}`)},
	{"AWS access key", regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"Slack token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}|\bxapp-[A-Za-z0-9-]{10,}`)},
	{"Anthropic-style API key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
	{"OpenAI-style API key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)},
}

// DecisionRecipient is one named recipient and its resolution: a participant address, a
// peer name, or the reason the name does not resolve.
type DecisionRecipient struct {
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty"`
	Resolved bool   `json:"resolved"`
	Reason   string `json:"reason,omitempty"`
}

// DecisionItem is the full record that decision_get returns.
type DecisionItem struct {
	Reference      string              `json:"reference"`
	Order          int64               `json:"order"`
	Title          string              `json:"title"`
	Where          string              `json:"where"`
	AgentText      string              `json:"agent_text"`
	Context        string              `json:"context"`
	Recommendation string              `json:"recommendation"`
	Kind           string              `json:"kind"`
	State          string              `json:"state"`
	Revision       int64               `json:"revision"`
	Commands       []string            `json:"commands"`
	HiddenCommands int                 `json:"hidden_commands,omitempty"`
	Recipients     []DecisionRecipient `json:"recipients"`
	AuthorFamily   string              `json:"author_family"`
	AuthorName     string              `json:"author_name"`
	// AuthorRepository is the repository of the author\'s participant, where the item is
	// recorded (#536); a plain peer\'s item has none.
	AuthorRepository string `json:"author_repository,omitempty"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
	ArchivedAt       int64  `json:"archived_at,omitempty"`
	// ClosedBy, ClosedAt and OutcomeNote record the last close of the work (chunk 02).
	ClosedBy    string `json:"closed_by,omitempty"`
	ClosedAt    int64  `json:"closed_at,omitempty"`
	OutcomeNote string `json:"outcome_note,omitempty"`
	// Verdict is the current verdict with its deliveries, and Verdicts the number of
	// verdicts in the item's history (decision_history pages them).
	Verdict  *DecisionVerdict `json:"verdict"`
	Verdicts int64            `json:"verdicts"`
	// Withdrawals are the newest agent withdrawals, newest first, and WithdrawalCount the
	// number in the item's history (#504). Withdrawn is set while the latest one is not
	// reopened.
	Withdrawals     []DecisionWithdrawal `json:"withdrawals,omitempty"`
	WithdrawalCount int64                `json:"withdrawal_count,omitempty"`
	// Record asks the reader to record the item's verdicts in the repository (#536).
	Record    *DecisionRecord `json:"record,omitempty"`
	Withdrawn bool            `json:"withdrawn,omitempty"`
}

// DecisionWithdrawal is one agent withdrawal of an item: the revision it withdrew, the
// withdrawing peer, the reason and time, and the reopen that undid it, if any.
type DecisionWithdrawal struct {
	ID         int64  `json:"id"`
	Revision   int64  `json:"revision"`
	By         string `json:"by"`
	Family     string `json:"family"`
	Reason     string `json:"reason"`
	At         int64  `json:"at"`
	ReopenedAt int64  `json:"reopened_at,omitempty"`
	ReopenedBy string `json:"reopened_by,omitempty"`
}

// DecisionSummary is one list row: never the full text.
type DecisionSummary struct {
	Reference string `json:"reference"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
	Revision  int64  `json:"revision"`
	Order     int64  `json:"order"`
	// Verdict is the current verdict, or empty (chunk 02, #537).
	Verdict string `json:"verdict,omitempty"`
	// Withdrawn is set while an agent's withdrawal archives the item (#504).
	Withdrawn bool `json:"withdrawn,omitempty"`
	id        int64
}

// DecisionPage is one keyset page of summaries in item ID order.
type DecisionPage struct {
	Items     []DecisionSummary `json:"items"`
	More      bool              `json:"more"`
	NextAfter *int64            `json:"next_after"`
}

// DecisionFields are the agent-written fields of an item. A nil field is absent.
type DecisionFields struct {
	Order          *int64    `json:"order"`
	Title          *string   `json:"title"`
	Where          *string   `json:"where"`
	AgentText      *string   `json:"agent_text"`
	Context        *string   `json:"context"`
	Recommendation *string   `json:"recommendation"`
	Kind           *string   `json:"kind"`
	Commands       *[]string `json:"commands"`
	Recipients     *[]string `json:"recipients"`
}

// DecisionCreated is the result of decision_create, also returned to a retry.
type DecisionCreated struct {
	Reference  string              `json:"reference"`
	Revision   int64               `json:"revision"`
	State      string              `json:"state"`
	Recipients []DecisionRecipient `json:"recipients"`
	Duplicate  bool                `json:"duplicate,omitempty"`
}

func decisionInvalid(field, rule string) error {
	return workError("invalid_request", field+" "+rule)
}

// checkLine accepts at most limit bytes of UTF-8 with no control character.
func checkLine(value, field string, limit int, required bool) error {
	if required && value == "" {
		return decisionInvalid(field, "is required")
	}
	if len(value) > limit || !utf8.ValidString(value) {
		return decisionInvalid(field, fmt.Sprintf("must be at most %d bytes of UTF-8 text", limit))
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return decisionInvalid(field, "must not contain control characters")
		}
	}
	return nil
}

// checkMarkdown accepts at most limit bytes of UTF-8 whose only control characters are
// newline and tab.
func checkMarkdown(value, field string, limit int) error {
	if len(value) > limit || !utf8.ValidString(value) {
		return decisionInvalid(field, fmt.Sprintf("must be at most %d bytes of UTF-8 text", limit))
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return decisionInvalid(field, "must not contain control characters other than newline and tab")
		}
	}
	return nil
}

// printableCommand reports whether a terminal-copy command is 1 to 512 bytes of printable
// ASCII (0x20-0x7E): no line break, tab, DEL, zero-width, bidirectional or look-alike
// character can reach the maintainer's terminal.
func printableCommand(command string) bool {
	if command == "" || len(command) > decisionCommandLimit {
		return false
	}
	for i := 0; i < len(command); i++ {
		if command[i] < 0x20 || command[i] > 0x7e {
			return false
		}
	}
	return true
}

func checkCredentials(value, field string) error {
	for _, format := range credentialFormats {
		if format.re.MatchString(value) {
			return workError("credential_refused", field+" holds a "+format.name+"; remove it")
		}
	}
	return nil
}

// validate checks every present field. create requires title and kind.
func (f DecisionFields) validate(create bool) error {
	if create && (f.Title == nil || f.Kind == nil) {
		return decisionInvalid("title and kind", "are required")
	}
	texts := map[string]*string{"title": f.Title, "where": f.Where}
	for _, field := range []string{"title", "where"} {
		if v := texts[field]; v != nil {
			limit := decisionTitleLimit
			if field == "where" {
				limit = decisionWhereLimit
			}
			if err := checkLine(*v, field, limit, field == "title"); err != nil {
				return err
			}
		}
	}
	markdown := []struct {
		name  string
		value *string
		limit int
	}{{"agent_text", f.AgentText, decisionAgentTextLimit}, {"context", f.Context, decisionContextLimit},
		{"recommendation", f.Recommendation, decisionRecommendationLimit}}
	for _, m := range markdown {
		if m.value != nil {
			if err := checkMarkdown(*m.value, m.name, m.limit); err != nil {
				return err
			}
		}
	}
	if f.Kind != nil && *f.Kind != "decision" && *f.Kind != "task" && *f.Kind != "fyi" {
		return decisionInvalid("kind", "must be decision, task or fyi")
	}
	if f.Commands != nil {
		if len(*f.Commands) > decisionCommandCount {
			return decisionInvalid("commands", fmt.Sprintf("holds at most %d entries", decisionCommandCount))
		}
		for _, c := range *f.Commands {
			if !printableCommand(c) {
				return decisionInvalid("commands", "entries must be 1 to 512 bytes of printable ASCII (0x20-0x7E)")
			}
		}
	}
	if f.Recipients != nil {
		if len(*f.Recipients) > decisionRecipientCount {
			return decisionInvalid("recipients", fmt.Sprintf("holds at most %d names", decisionRecipientCount))
		}
		for _, r := range *f.Recipients {
			if err := checkLine(r, "recipients", decisionRecipientLimit, true); err != nil {
				return err
			}
		}
	}
	for _, t := range f.texts() {
		if err := checkCredentials(t.value, t.name); err != nil {
			return err
		}
	}
	return nil
}

type namedText struct{ name, value string }

func (f DecisionFields) texts() []namedText {
	var out []namedText
	for _, t := range []struct {
		name  string
		value *string
	}{{"title", f.Title}, {"where", f.Where}, {"agent_text", f.AgentText}, {"context", f.Context}, {"recommendation", f.Recommendation}} {
		if t.value != nil {
			out = append(out, namedText{t.name, *t.value})
		}
	}
	if f.Commands != nil {
		for _, c := range *f.Commands {
			out = append(out, namedText{"commands", c})
		}
	}
	if f.Recipients != nil {
		for _, r := range *f.Recipients {
			out = append(out, namedText{"recipients", r})
		}
	}
	return out
}

func checkReference(reference string) error {
	if !decisionReference.MatchString(reference) {
		return decisionInvalid("reference", "must be 1 to 32 characters of A-Z, a-z, 0-9 and -")
	}
	return checkCredentials(reference, "reference")
}

// resolveRecipient resolves a name to a participant address or a peer name.
func resolveRecipient(ctx context.Context, q querier, name string) (kind, reason string, err error) {
	var names string
	err = q.QueryRowContext(ctx, `SELECT kind FROM names WHERE name=?`, name).Scan(&names)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "unknown_name", nil
	}
	if err != nil {
		return "", "", err
	}
	if names == "alias" {
		return "participant", "", nil
	}
	return "peer", "", nil
}

func decisionRecipients(ctx context.Context, q querier, names []string) ([]DecisionRecipient, error) {
	out := make([]DecisionRecipient, 0, len(names))
	for _, name := range names {
		kind, reason, err := resolveRecipient(ctx, q, name)
		if err != nil {
			return nil, err
		}
		out = append(out, DecisionRecipient{Name: name, Kind: kind, Resolved: kind != "", Reason: reason})
	}
	return out, nil
}

func storeRecipients(ctx context.Context, tx *writeTx, id int64, recipients []DecisionRecipient) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM decision_recipients WHERE item_id=?`, id); err != nil {
		return tx.fail(err)
	}
	for i, r := range recipients {
		if _, err := tx.ExecContext(ctx, `INSERT INTO decision_recipients(item_id,ordinal,name,kind,reason) VALUES (?,?,?,?,?)`,
			id, i, r.Name, r.Kind, r.Reason); err != nil {
			return tx.fail(err)
		}
	}
	return nil
}

func decisionFingerprint(reference string, f DecisionFields, deadline float64) string {
	data, _ := json.Marshal(struct {
		Reference string         `json:"reference"`
		Fields    DecisionFields `json:"fields"`
		Deadline  string         `json:"deadline"`
	}{reference, f, strconv.FormatFloat(deadline, 'g', -1, 64)})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func decisionRepeat(ctx context.Context, q querier, caller Key, key, print string) (*DecisionCreated, error) {
	var stored, result string
	err := q.QueryRowContext(ctx, `SELECT fingerprint,result FROM decision_replays WHERE family=? AND caller_id=? AND key=?`,
		caller.Family, caller.ID, key).Scan(&stored, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if stored != print {
		return nil, workError("idempotency_conflict", "this key was used for a different request")
	}
	var original DecisionCreated
	if err := json.Unmarshal([]byte(result), &original); err != nil {
		return nil, err
	}
	original.Duplicate = true
	return &original, nil
}

// DecisionCreate creates an active item as the caller. key and deadline make a retry
// return the first result.
func (s *Store) DecisionCreate(ctx context.Context, caller Key, reference string, f DecisionFields, key string, deadline float64) (DecisionCreated, error) {
	if !validKey(caller.Family, caller.ID) {
		return DecisionCreated{}, ErrInvalid
	}
	if err := checkReference(reference); err != nil {
		return DecisionCreated{}, err
	}
	if err := f.validate(true); err != nil {
		return DecisionCreated{}, err
	}
	if err := checkLine(key, "key", decisionKeyLimit, true); err != nil {
		return DecisionCreated{}, err
	}
	clock := s.clock()
	if math.IsNaN(deadline) || math.IsInf(deadline, 0) || deadline > clock+s.limits().idemTTL {
		return DecisionCreated{}, decisionInvalid("deadline", fmt.Sprintf("must be a Unix time in seconds at most %.0f s ahead", s.limits().idemTTL))
	}
	if deadline <= clock {
		return DecisionCreated{}, workError("retry_deadline_expired", "the retry deadline has passed")
	}
	print := decisionFingerprint(reference, f, deadline)
	// Create keys keep the key space of schema 25, so a receipt stored before the upgrade
	// still answers its retry. Close keys carry a NUL prefix, which no caller key can hold
	// (checkLine refuses control characters), so a create and a close may use the same key.
	// A repeat writes nothing, so it is answered before write admission.
	read, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DecisionCreated{}, err
	}
	if err := active(ctx, read, s.now().UnixMilli(), caller); err != nil {
		read.Rollback()
		return DecisionCreated{}, err
	}
	original, err := decisionRepeat(ctx, read, caller, key, print)
	read.Rollback()
	if err != nil || original != nil {
		return orCreated(original), err
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return DecisionCreated{}, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx.Tx, now, caller); err != nil {
		return DecisionCreated{}, err
	}
	if original, err := decisionRepeat(ctx, tx.Tx, caller, key, print); err != nil || original != nil {
		return orCreated(original), err
	}
	// A deep-archived item keeps its reference reserved (chunk 02).
	var taken int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM decision_items WHERE reference=?)+
		(SELECT COUNT(*) FROM decision_deep_archive WHERE reference=?)`, reference, reference).Scan(&taken); err != nil {
		return DecisionCreated{}, err
	}
	if taken > 0 {
		return DecisionCreated{}, workError("reference_in_use", "another decision item uses this reference")
	}
	author, err := peerOf(ctx, tx.Tx, caller.Family, caller.ID)
	if err != nil {
		return DecisionCreated{}, err
	}
	// The author's participant lets a later session of it withdraw the item (#504).
	participant, err := authorParticipantOf(ctx, tx.Tx, caller)
	if err != nil {
		return DecisionCreated{}, err
	}
	names := []string{}
	if f.Recipients != nil {
		names = *f.Recipients
	}
	recipients, err := decisionRecipients(ctx, tx.Tx, names)
	if err != nil {
		return DecisionCreated{}, err
	}
	commands := []string{}
	if f.Commands != nil {
		commands = *f.Commands
	}
	encoded, _ := json.Marshal(commands)
	inserted, err := tx.ExecContext(ctx, `INSERT INTO decision_items(reference,item_order,title,location,agent_text,context,recommendation,
		kind,state,commands,revision,author_family,author_name,author_participant,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,'active',?,1,?,?,?,?,?)`,
		reference, derefOrder(f.Order), *f.Title, deref(f.Where), deref(f.AgentText), deref(f.Context),
		deref(f.Recommendation), *f.Kind, string(encoded), caller.Family, author, participant, now, now)
	if err != nil {
		return DecisionCreated{}, tx.fail(err)
	}
	id, err := inserted.LastInsertId()
	if err != nil {
		return DecisionCreated{}, err
	}
	if err := storeRecipients(ctx, tx, id, recipients); err != nil {
		return DecisionCreated{}, err
	}
	if err := bumpChange(ctx, tx, changeDecisions); err != nil {
		return DecisionCreated{}, tx.fail(err)
	}
	created := DecisionCreated{Reference: reference, Revision: 1, State: "active", Recipients: recipients}
	data, _ := json.Marshal(created)
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_replays(family,caller_id,key,fingerprint,deadline,result) VALUES (?,?,?,?,?,?)`,
		caller.Family, caller.ID, key, print, deadline, string(data)); err != nil {
		return DecisionCreated{}, tx.fail(err)
	}
	return created, tx.Commit()
}

func orCreated(c *DecisionCreated) DecisionCreated {
	if c == nil {
		return DecisionCreated{}
	}
	return *c
}

func derefOrder(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// DecisionEdit changes the present fields of an item at revision ifRevision. An edit of a
// returned, in-progress or archived item reopens it for a new verdict: the item becomes active, its
// current verdict is superseded and any deep-archive mark is cleared, in the same
// transaction. The verdict's deliveries do not change; an unacknowledged one stays queued.
func (s *Store) DecisionEdit(ctx context.Context, caller Key, reference string, ifRevision int64, f DecisionFields) (DecisionItem, error) {
	if !validKey(caller.Family, caller.ID) {
		return DecisionItem{}, ErrInvalid
	}
	if err := checkReference(reference); err != nil {
		return DecisionItem{}, err
	}
	if f == (DecisionFields{}) {
		return DecisionItem{}, decisionInvalid("edit", "names no field to change")
	}
	if err := f.validate(false); err != nil {
		return DecisionItem{}, err
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return DecisionItem{}, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx.Tx, now, caller); err != nil {
		return DecisionItem{}, err
	}
	id, item, err := decisionRow(ctx, tx.Tx, reference)
	if err != nil {
		return DecisionItem{}, err
	}
	if item.Revision != ifRevision {
		return DecisionItem{}, WorkRefusal{Code: "stale_revision", Message: "the decision item changed; read it again",
			Details: map[string]any{"current_revision": item.Revision}}
	}
	if item.State != "active" {
		if _, err := tx.ExecContext(ctx, `UPDATE decision_verdicts SET superseded_at=? WHERE item_id=? AND superseded_at=0`, now, id); err != nil {
			return DecisionItem{}, tx.fail(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE decision_items SET state='active',archived_at=0,mark_revision=0,mark_file='',mark_offset=0 WHERE id=?`, id); err != nil {
			return DecisionItem{}, tx.fail(err)
		}
		// An edit that reopens a withdrawn item ends its withdrawal as a restore does (#504).
		editor, err := peerOf(ctx, tx.Tx, caller.Family, caller.ID)
		if err != nil {
			return DecisionItem{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE decision_withdrawals SET reopened_at=?,reopened_by=? WHERE item_id=? AND reopened_at=0`, now, editor, id); err != nil {
			return DecisionItem{}, tx.fail(err)
		}
	}
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	if f.Order != nil {
		item.Order = *f.Order
	}
	set(&item.Title, f.Title)
	set(&item.Where, f.Where)
	set(&item.AgentText, f.AgentText)
	set(&item.Context, f.Context)
	set(&item.Recommendation, f.Recommendation)
	set(&item.Kind, f.Kind)
	if f.Commands != nil {
		item.Commands = *f.Commands
	}
	encoded, _ := json.Marshal(item.Commands)
	item.Revision++
	item.UpdatedAt = now
	if _, err := tx.ExecContext(ctx, `UPDATE decision_items SET item_order=?,title=?,location=?,agent_text=?,context=?,recommendation=?,
		kind=?,commands=?,revision=?,updated_at=? WHERE id=?`, item.Order, item.Title, item.Where, item.AgentText, item.Context,
		item.Recommendation, item.Kind, string(encoded), item.Revision, now, id); err != nil {
		return DecisionItem{}, tx.fail(err)
	}
	if err := bumpChange(ctx, tx, changeDecisions); err != nil {
		return DecisionItem{}, tx.fail(err)
	}
	if f.Recipients != nil {
		recipients, err := decisionRecipients(ctx, tx.Tx, *f.Recipients)
		if err != nil {
			return DecisionItem{}, err
		}
		if err := storeRecipients(ctx, tx, id, recipients); err != nil {
			return DecisionItem{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return DecisionItem{}, err
	}
	return s.decisionRead(ctx, reference)
}

// decisionRow reads an item's stored fields, without recipients.
func decisionRow(ctx context.Context, q querier, reference string) (int64, DecisionItem, error) {
	var id int64
	var item DecisionItem
	var commands string
	err := q.QueryRowContext(ctx, `SELECT id,reference,item_order,title,location,agent_text,context,recommendation,kind,state,
		commands,revision,author_family,author_name,created_at,updated_at,archived_at,closed_by,closed_at,outcome_note,verdict_count
		FROM decision_items WHERE reference=?`, reference).
		Scan(&id, &item.Reference, &item.Order, &item.Title, &item.Where, &item.AgentText, &item.Context, &item.Recommendation,
			&item.Kind, &item.State, &commands, &item.Revision, &item.AuthorFamily, &item.AuthorName, &item.CreatedAt, &item.UpdatedAt,
			&item.ArchivedAt, &item.ClosedBy, &item.ClosedAt, &item.OutcomeNote, &item.Verdicts)
	if errors.Is(err, sql.ErrNoRows) {
		// A deep-archived reference is answered from its locator, without reading the log.
		at, moved, err := deepArchived(ctx, q, reference)
		if err != nil {
			return 0, item, err
		}
		if moved {
			return 0, item, WorkRefusal{Code: "deep_archived", Message: "the item moved to the deep archive; restore it with decision_restore",
				Details: map[string]any{"moved_at": at, "restore": "decision_restore"}}
		}
		return 0, item, workError("not_found", "no decision item has this reference")
	}
	if err != nil {
		return 0, item, err
	}
	if json.Unmarshal([]byte(commands), &item.Commands) != nil {
		return 0, item, workError("decision_unavailable", "the stored commands are invalid")
	}
	return id, item, nil
}

// decisionItem reads the full record. Commands that fail the printable-ASCII rule are
// hidden; recipients are resolved again.
func decisionItem(ctx context.Context, q querier, reference string) (DecisionItem, error) {
	id, item, err := decisionRow(ctx, q, reference)
	if err != nil {
		return item, err
	}
	shown := make([]string, 0, len(item.Commands))
	for _, c := range item.Commands {
		if printableCommand(c) {
			shown = append(shown, c)
		} else {
			item.HiddenCommands++
		}
	}
	item.Commands = shown
	// The author's participant holds the repository that the item was created in (#536).
	var participant string
	if err := q.QueryRowContext(ctx, `SELECT author_participant FROM decision_items WHERE id=?`, id).Scan(&participant); err != nil {
		return item, err
	}
	if parts := strings.Split(participant, "\x1f"); len(parts) == 3 {
		item.AuthorRepository = parts[1]
	}
	rows, err := q.QueryContext(ctx, `SELECT name FROM decision_recipients WHERE item_id=? ORDER BY ordinal`, id)
	if err != nil {
		return item, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return item, err
		}
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return item, err
	}
	if item.Recipients, err = decisionRecipients(ctx, q, names); err != nil {
		return item, err
	}
	// The verdict count is stored on the item, so a read does not count the history.
	if item.Verdict, err = currentVerdict(ctx, q, id, true); err != nil {
		return item, err
	}
	if item.Verdict != nil && recordedVerdict(item.Verdict.Verdict) {
		item.Record = &DecisionRecord{Path: DecisionRecordFile, Instruction: decisionRecordInstruction,
			Entry: DecisionRecordEntry(item.Reference, item.Where, *item.Verdict)}
	}
	item.Withdrawals, item.WithdrawalCount, err = decisionWithdrawals(ctx, q, id)
	item.Withdrawn = item.State == "archived" && len(item.Withdrawals) > 0 && item.Withdrawals[0].ReopenedAt == 0
	return item, err
}

// decisionWithdrawalItems bounds the withdrawals that an item read shows.
const decisionWithdrawalItems = 8

// decisionWithdrawals reads an item's newest withdrawals and their number.
func decisionWithdrawals(ctx context.Context, q querier, id int64) ([]DecisionWithdrawal, int64, error) {
	var total int64
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_withdrawals WHERE item_id=?`, id).Scan(&total); err != nil || total == 0 {
		return nil, 0, err
	}
	out, err := scanWithdrawals(q.QueryContext(ctx, `SELECT id,revision,name,family,reason,at,reopened_at,reopened_by FROM decision_withdrawals
		WHERE item_id=? ORDER BY id DESC LIMIT `+strconv.Itoa(decisionWithdrawalItems), id))
	return out, total, err
}

func scanWithdrawals(rows *sql.Rows, err error) ([]DecisionWithdrawal, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DecisionWithdrawal{}
	for rows.Next() {
		var w DecisionWithdrawal
		if err := rows.Scan(&w.ID, &w.Revision, &w.By, &w.Family, &w.Reason, &w.At, &w.ReopenedAt, &w.ReopenedBy); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// withdrawalHistory reads one page of an item's withdrawals in ID order after after, at most
// limit; an item read shows only the newest (#504).
func withdrawalHistory(ctx context.Context, q querier, reference string, after int64, limit int) (DecisionHistoryPage, error) {
	id, _, err := decisionRow(ctx, q, reference)
	if err != nil {
		return DecisionHistoryPage{}, err
	}
	withdrawals, err := scanWithdrawals(q.QueryContext(ctx, `SELECT id,revision,name,family,reason,at,reopened_at,reopened_by FROM decision_withdrawals
		WHERE item_id=? AND id>? ORDER BY id LIMIT ?`, id, after, limit+1))
	if err != nil {
		return DecisionHistoryPage{}, err
	}
	page := DecisionHistoryPage{Reference: reference, Verdicts: []DecisionVerdict{}, Withdrawals: withdrawals}
	if len(withdrawals) > limit {
		page.Withdrawals = withdrawals[:limit]
		last := page.Withdrawals[limit-1].ID
		page.More, page.NextAfter = true, &last
	}
	return page, nil
}

func (s *Store) decisionRead(ctx context.Context, reference string) (DecisionItem, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DecisionItem{}, err
	}
	defer tx.Rollback()
	return decisionItem(ctx, tx, reference)
}

// DecisionGet returns the full record for an active caller. Reading moves no cursor.
func (s *Store) DecisionGet(ctx context.Context, caller Key, reference string) (DecisionItem, error) {
	if err := checkReference(reference); err != nil {
		return DecisionItem{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DecisionItem{}, err
	}
	defer tx.Rollback()
	if err := active(ctx, tx, s.now().UnixMilli(), caller); err != nil {
		return DecisionItem{}, err
	}
	return decisionItem(ctx, tx, reference)
}

var decisionFilters = map[string]bool{"waiting": true, "active": true, "returned": true, "in_progress": true, "archived": true, "mine": true}

// DecisionList returns one page of summaries for an active caller.
func (s *Store) DecisionList(ctx context.Context, caller Key, filter string, after int64, limit int) (DecisionPage, error) {
	if !decisionFilters[filter] {
		return DecisionPage{}, decisionInvalid("filter", "must be waiting, active, returned, in_progress, archived or mine")
	}
	if after < 0 || limit < 0 {
		return DecisionPage{}, decisionInvalid("after and limit", "must not be negative")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DecisionPage{}, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx, now, caller); err != nil {
		return DecisionPage{}, err
	}
	var targets []string
	if filter == "mine" {
		peer, err := peerOf(ctx, tx, caller.Family, caller.ID)
		if err != nil {
			return DecisionPage{}, err
		}
		address, err := heldAddress(ctx, tx, now, caller)
		if err != nil {
			return DecisionPage{}, err
		}
		for _, name := range []string{peer, address} {
			if name != "" {
				targets = append(targets, name)
			}
		}
	}
	return decisionPage(ctx, tx, filter, targets, after, limit)
}

// decisionPage reads one index range in item ID order. The page stops at limit items or
// before the summary that would take the serialized reply past decisionPageBytes, and
// always holds at least one summary.
// decisionSummaryColumns are a summary's columns: the current verdict, and whether an
// agent's withdrawal archives the item now (#504).
const decisionSummaryColumns = `id,reference,title,kind,state,revision,item_order,COALESCE((SELECT CASE WHEN v.superseded_at=0 THEN v.verdict ELSE '' END
	FROM decision_verdicts v WHERE v.item_id=decision_items.id ORDER BY v.id DESC LIMIT 1),''),
	state='archived' AND COALESCE((SELECT w.reopened_at=0 FROM decision_withdrawals w WHERE w.item_id=decision_items.id ORDER BY w.id DESC LIMIT 1),0)`

func decisionPage(ctx context.Context, q querier, filter string, targets []string, after int64, limit int) (DecisionPage, error) {
	if limit <= 0 || limit > decisionPageItems {
		limit = decisionPageItems
	}
	var ids []int64
	var summaries []DecisionSummary
	if filter == "mine" {
		seen := map[int64]bool{}
		for _, t := range targets {
			// DISTINCT: an item may name the caller more than once.
			rows, err := q.QueryContext(ctx, `SELECT DISTINCT item_id FROM decision_recipients WHERE name=? AND item_id>?
				ORDER BY item_id LIMIT ?`, t, after, limit+1)
			if err != nil {
				return DecisionPage{}, err
			}
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return DecisionPage{}, err
				}
				if !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return DecisionPage{}, err
			}
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if len(ids) > limit+1 {
			ids = ids[:limit+1]
		}
		for _, id := range ids {
			var s DecisionSummary
			if err := q.QueryRowContext(ctx, `SELECT `+decisionSummaryColumns+` FROM decision_items WHERE id=?`, id).
				Scan(&s.id, &s.Reference, &s.Title, &s.Kind, &s.State, &s.Revision, &s.Order, &s.Verdict, &s.Withdrawn); err != nil {
				return DecisionPage{}, err
			}
			summaries = append(summaries, s)
		}
	} else {
		query, args := `SELECT `+decisionSummaryColumns+` FROM decision_items WHERE state=? AND id>? ORDER BY id LIMIT ?`,
			[]any{filter, after, limit + 1}
		if filter == "waiting" {
			query, args = `SELECT `+decisionSummaryColumns+` FROM decision_items WHERE state='active'
				AND kind!='fyi' AND id>? ORDER BY id LIMIT ?`, []any{after, limit + 1}
		}
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return DecisionPage{}, err
		}
		for rows.Next() {
			var s DecisionSummary
			if err := rows.Scan(&s.id, &s.Reference, &s.Title, &s.Kind, &s.State, &s.Revision, &s.Order, &s.Verdict, &s.Withdrawn); err != nil {
				rows.Close()
				return DecisionPage{}, err
			}
			summaries = append(summaries, s)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return DecisionPage{}, err
		}
	}
	page := DecisionPage{Items: []DecisionSummary{}}
	// The envelope as the route sends it, with the longest next_after.
	largest := int64(math.MaxInt64)
	envelope, _ := json.Marshal(map[string]any{"ok": true, "result": DecisionPage{Items: []DecisionSummary{}, More: true, NextAfter: &largest}})
	used := len(envelope) + 1
	for i, s := range summaries {
		data, _ := json.Marshal(s)
		size := len(data)
		if len(page.Items) > 0 {
			size++
		}
		if i == limit || (len(page.Items) > 0 && used+size > decisionPageBytes) {
			last := page.Items[len(page.Items)-1].id
			page.More, page.NextAfter = true, &last
			break
		}
		used += size
		page.Items = append(page.Items, s)
	}
	return page, nil
}

// cullDecisionReplays deletes, in one bounded control write, create replays whose retry
// deadline has passed.
func (s *Store) cullDecisionReplays(ctx context.Context) error {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM decision_replays WHERE rowid IN (SELECT rowid FROM decision_replays WHERE deadline<? LIMIT ?)`,
		s.clock(), cullBatch); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}
