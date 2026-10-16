package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// Decision items (#206, chunk 01). All data is synthetic.

func decisionFields(title, kind string) DecisionFields {
	return DecisionFields{Title: ptr(title), Kind: ptr(kind)}
}

func mustCreate(t *testing.T, s *Store, caller Key, reference string, f DecisionFields) DecisionCreated {
	t.Helper()
	created, err := s.DecisionCreate(context.Background(), caller, reference, f, "key-"+reference, s.clock()+600)
	if err != nil {
		t.Fatalf("create %s: %v", reference, err)
	}
	return created
}

func refusalCode(err error) string {
	var refused WorkRefusal
	if errors.As(err, &refused) {
		return refused.Code
	}
	return errorCode(err)
}

func TestDecisionCreateEditGetListEveryFamily(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for i, family := range []string{"claude", "codex", "agy", "opencode", "grok", "deepseek"} {
		session := join(t, s, family, "synthetic-decision-"+family, "")
		caller := Key{family, session.ID}
		reference := fmt.Sprintf("D-%d", i+1)
		f := decisionFields("Synthetic choice for "+family, "decision")
		f.Order, f.Context, f.Recommendation = ptr(int64(i)), ptr("Line one.\n\n- item\n\tcode"), ptr("Approve.")
		f.Commands, f.Recipients = &[]string{"go test ./..."}, &[]string{session.Name}
		created := mustCreate(t, s, caller, reference, f)
		if created.Revision != 1 || created.State != "active" || !created.Recipients[0].Resolved || created.Recipients[0].Kind != "peer" {
			t.Fatalf("%s create: %+v", family, created)
		}
		edited, err := s.DecisionEdit(ctx, caller, reference, 1, DecisionFields{Title: ptr("Edited " + family)})
		if err != nil || edited.Revision != 2 || edited.Title != "Edited "+family || edited.Context != *f.Context {
			t.Fatalf("%s edit: %+v %v", family, edited, err)
		}
		got, err := s.DecisionGet(ctx, caller, reference)
		if err != nil || got.AuthorFamily != family || got.AuthorName != session.Name || len(got.Commands) != 1 || got.Order != int64(i) {
			t.Fatalf("%s get: %+v %v", family, got, err)
		}
		page, err := s.DecisionList(ctx, caller, "waiting", 0, 0)
		if err != nil || len(page.Items) != i+1 || page.More {
			t.Fatalf("%s list: %+v %v", family, page, err)
		}
		mine, err := s.DecisionList(ctx, caller, "mine", 0, 0)
		if err != nil || len(mine.Items) != 1 || mine.Items[0].Reference != reference {
			t.Fatalf("%s mine: %+v %v", family, mine, err)
		}
	}
}

func TestDecisionCallerAndFieldRefusals(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	session := join(t, s, "codex", "synthetic-decision-refusals", "")
	caller := Key{"codex", session.ID}
	if _, err := s.DecisionCreate(ctx, maintainerKey, "M-1", decisionFields("t", "fyi"), "k", s.clock()+60); !errors.Is(err, ErrInvalid) {
		t.Fatalf("maintainer caller: %v", err)
	}
	if _, err := s.DecisionList(ctx, maintainerKey, "waiting", 0, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("maintainer list: %v", err)
	}
	guessed := Key{"codex", "synthetic-guessed"}
	if _, err := s.DecisionCreate(ctx, guessed, "G-1", decisionFields("t", "fyi"), "k", s.clock()+60); !errors.Is(err, ErrCallerInactive) {
		t.Fatalf("guessed caller: %v", err)
	}
	if _, err := s.DecisionGet(ctx, guessed, "G-1"); !errors.Is(err, ErrCallerInactive) {
		t.Fatalf("guessed get: %v", err)
	}
	bad := map[string]struct {
		reference string
		f         DecisionFields
		code      string
	}{
		"reference":       {"bad ref", decisionFields("t", "fyi"), "invalid_request"},
		"long reference":  {strings.Repeat("a", 33), decisionFields("t", "fyi"), "invalid_request"},
		"no title":        {"R-1", DecisionFields{Kind: ptr("fyi")}, "invalid_request"},
		"title control":   {"R-1", decisionFields("a\nb", "fyi"), "invalid_request"},
		"title tab":       {"R-1", decisionFields("a\tb", "fyi"), "invalid_request"},
		"long title":      {"R-1", decisionFields(strings.Repeat("a", 257), "fyi"), "invalid_request"},
		"kind":            {"R-1", decisionFields("t", "verdict"), "invalid_request"},
		"invalid utf8":    {"R-1", decisionFields("a\xffb", "fyi"), "invalid_request"},
		"markdown cr":     {"R-1", DecisionFields{Title: ptr("t"), Kind: ptr("fyi"), Context: ptr("a\rb")}, "invalid_request"},
		"markdown nul":    {"R-1", DecisionFields{Title: ptr("t"), Kind: ptr("fyi"), Recommendation: ptr("a\x00b")}, "invalid_request"},
		"long context":    {"R-1", DecisionFields{Title: ptr("t"), Kind: ptr("fyi"), Context: ptr(strings.Repeat("a", 16<<10+1))}, "invalid_request"},
		"where newline":   {"R-1", DecisionFields{Title: ptr("t"), Kind: ptr("fyi"), Where: ptr("a\nb")}, "invalid_request"},
		"recipient ctl":   {"R-1", DecisionFields{Title: ptr("t"), Kind: ptr("fyi"), Recipients: &[]string{"a\x1bb"}}, "invalid_request"},
		"many recipients": {"R-1", DecisionFields{Title: ptr("t"), Kind: ptr("fyi"), Recipients: ptr(make([]string, 33))}, "invalid_request"},
	}
	for name, command := range map[string]string{"newline": "ls\nrm", "tab": "ls\t-l", "del": "ls\x7f", "zero width": "ls​ -l",
		"bidi": "ls ‮-l", "look-alike": "ls -а", "empty": "", "long": strings.Repeat("a", 513)} {
		bad["command "+name] = struct {
			reference string
			f         DecisionFields
			code      string
		}{"R-1", DecisionFields{Title: ptr("t"), Kind: ptr("fyi"), Commands: &[]string{command}}, "invalid_request"}
	}
	bad["nine commands"] = struct {
		reference string
		f         DecisionFields
		code      string
	}{"R-1", DecisionFields{Title: ptr("t"), Kind: ptr("fyi"), Commands: ptr(strings.Split("a,b,c,d,e,f,g,h,i", ","))}, "invalid_request"}
	for name, value := range map[string]string{
		"private key": "-----BEGIN OPENSSH PRIVATE KEY-----",
		"github":      "ghp_" + strings.Repeat("A1", 18),
		"github pat":  "github_pat_" + strings.Repeat("a", 30),
		"aws":         "AKIA" + strings.Repeat("Q", 16),
		"slack":       "xoxb-" + strings.Repeat("1", 12),
		"openai":      "sk-" + strings.Repeat("x", 24),
		"anthropic":   "sk-ant-" + strings.Repeat("x", 24),
	} {
		for _, field := range []string{"context", "title", "command"} {
			f := decisionFields("t", "fyi")
			switch field {
			case "context":
				f.Context = ptr("Use " + value + " here.")
			case "title":
				f.Title = ptr("Key " + value)
			case "command":
				f.Commands = &[]string{"export TOKEN=" + value}
			}
			bad["credential "+name+" in "+field] = struct {
				reference string
				f         DecisionFields
				code      string
			}{"R-1", f, "credential_refused"}
		}
	}
	for name, c := range bad {
		if _, err := s.DecisionCreate(ctx, caller, c.reference, c.f, "k-"+name, s.clock()+60); refusalCode(err) != c.code {
			t.Errorf("%s: %v, want %s", name, err, c.code)
		}
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_items`) != 0 || count(t, s, `SELECT COUNT(*) FROM decision_replays`) != 0 {
		t.Fatal("a refused create wrote rows")
	}
	// Prose that names a key format without holding a key is accepted.
	mustCreate(t, s, caller, "R-2", DecisionFields{Title: ptr("Rotate the sk- key"), Kind: ptr("decision"), Context: ptr("A ghp_ token was rotated.")})
	if _, err := s.DecisionCreate(ctx, caller, "R-2", decisionFields("t", "fyi"), "other-key", s.clock()+60); refusalCode(err) != "reference_in_use" {
		t.Fatalf("reference reuse: %v", err)
	}
	if _, err := s.DecisionEdit(ctx, caller, "R-2", 9, DecisionFields{Title: ptr("x")}); refusalCode(err) != "stale_revision" {
		t.Fatalf("stale: %v", err)
	}
	if _, err := s.DecisionEdit(ctx, caller, "R-2", 1, DecisionFields{}); refusalCode(err) != "invalid_request" {
		t.Fatalf("empty edit: %v", err)
	}
	if _, err := s.DecisionEdit(ctx, caller, "R-9", 1, DecisionFields{Title: ptr("x")}); refusalCode(err) != "not_found" {
		t.Fatalf("missing edit: %v", err)
	}
	if _, err := s.DecisionGet(ctx, caller, "R-9"); refusalCode(err) != "not_found" {
		t.Fatalf("missing get: %v", err)
	}
	if _, err := fixtureExec(s, `UPDATE decision_items SET state='in_progress' WHERE reference='R-2'`); err != nil {
		t.Fatal(err)
	}
	// An edit of a decided item reopens it (chunk 02).
	if reopened, err := s.DecisionEdit(ctx, caller, "R-2", 1, DecisionFields{Title: ptr("x")}); err != nil || reopened.State != "active" {
		t.Fatalf("reopen edit: %+v %v", reopened, err)
	}
	if _, err := s.DecisionList(ctx, caller, "everything", 0, 0); refusalCode(err) != "invalid_request" {
		t.Fatalf("filter: %v", err)
	}
}

func TestDecisionCreateRetryReturnsFirstResult(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	session := join(t, s, "claude", "synthetic-decision-retry", "")
	caller := Key{"claude", session.ID}
	deadline := s.clock() + 600
	f := decisionFields("Retry", "decision")
	first, err := s.DecisionCreate(ctx, caller, "K-1", f, "fixed", deadline)
	if err != nil || first.Duplicate {
		t.Fatalf("first: %+v %v", first, err)
	}
	// A lost reply: the retry is answered before write admission, even from a full store.
	s.storage.blocked = "synthetic blocked write admission"
	again, err := s.DecisionCreate(ctx, caller, "K-1", decisionFields("Retry", "decision"), "fixed", deadline)
	s.storage.blocked = ""
	if err != nil || !again.Duplicate || again.Reference != "K-1" || again.Revision != 1 {
		t.Fatalf("retry: %+v %v", again, err)
	}
	if _, err := s.DecisionCreate(ctx, caller, "K-1", decisionFields("Different", "decision"), "fixed", deadline); refusalCode(err) != "idempotency_conflict" {
		t.Fatalf("changed request: %v", err)
	}
	if _, err := s.DecisionCreate(ctx, caller, "K-2", f, "late", s.clock()-1); refusalCode(err) != "retry_deadline_expired" {
		t.Fatalf("late: %v", err)
	}
	if _, err := s.DecisionCreate(ctx, caller, "K-2", f, "far", s.clock()+s.limits().idemTTL+60); refusalCode(err) != "invalid_request" {
		t.Fatalf("far: %v", err)
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_items`) != 1 {
		t.Fatal("a retry created a second item")
	}
	// Expired replays are culled in the sweep; the item stays.
	if _, err := fixtureExec(s, `UPDATE decision_replays SET deadline=1`); err != nil {
		t.Fatal(err)
	}
	if err := s.cullDecisionReplays(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_replays`) != 0 || count(t, s, `SELECT COUNT(*) FROM decision_items`) != 1 {
		t.Fatal("cull")
	}
}

func TestDecisionRecipientsAndMine(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	repo := testRepo(t)
	holder := joinAs(t, s, "codex", "synthetic-decision-holder", repo, "")
	if holder.Alias == "" {
		t.Fatalf("no participant address: %+v", holder)
	}
	other := join(t, s, "claude", "synthetic-decision-other", "")
	callerHolder, callerOther := Key{"codex", holder.ID}, Key{"claude", other.ID}
	mustCreate(t, s, callerOther, "P-1", DecisionFields{Title: ptr("To participant"), Kind: ptr("decision"), Recipients: &[]string{holder.Alias}})
	mustCreate(t, s, callerOther, "P-2", DecisionFields{Title: ptr("To peer"), Kind: ptr("fyi"), Recipients: &[]string{holder.Name, "synthetic-nobody"}})
	mustCreate(t, s, callerOther, "P-3", DecisionFields{Title: ptr("To other"), Kind: ptr("decision"), Recipients: &[]string{other.Name}})
	mustCreate(t, s, callerOther, "P-4", DecisionFields{Title: ptr("Nobody"), Kind: ptr("decision")})
	mine, err := s.DecisionList(ctx, callerHolder, "mine", 0, 0)
	if err != nil || len(mine.Items) != 2 || mine.Items[0].Reference != "P-1" || mine.Items[1].Reference != "P-2" {
		t.Fatalf("mine: %+v %v", mine, err)
	}
	got, err := s.DecisionGet(ctx, callerHolder, "P-2")
	if err != nil || len(got.Recipients) != 2 || got.Recipients[0].Kind != "peer" || got.Recipients[1].Resolved || got.Recipients[1].Reason != "unknown_name" {
		t.Fatalf("recipients: %+v %v", got.Recipients, err)
	}
	got, err = s.DecisionGet(ctx, callerHolder, "P-1")
	if err != nil || got.Recipients[0].Kind != "participant" {
		t.Fatalf("participant: %+v %v", got.Recipients, err)
	}
	// An item that names the caller several times is one row of mine, and the page after it
	// is still reached.
	mustCreate(t, s, callerOther, "P-5", DecisionFields{Title: ptr("Named three times"), Kind: ptr("decision"), Recipients: &[]string{holder.Name, holder.Name, holder.Alias}})
	var walked []string
	for after := int64(0); ; {
		page, err := s.DecisionList(ctx, callerHolder, "mine", after, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			walked = append(walked, item.Reference)
		}
		if !page.More {
			break
		}
		after = *page.NextAfter
	}
	if strings.Join(walked, ",") != "P-1,P-2,P-5" {
		t.Fatalf("mine walk: %v", walked)
	}
	// A name that resolves only after the write still names its holder in mine.
	later := testRepo(t)
	address := AddressBase("codex", later, later, "rx")
	mustCreate(t, s, callerOther, "P-6", DecisionFields{Title: ptr("Before registration"), Kind: ptr("decision"), Recipients: &[]string{address}})
	created, err := s.DecisionGet(ctx, callerOther, "P-6")
	if err != nil || created.Recipients[0].Resolved {
		t.Fatalf("unregistered recipient: %+v %v", created.Recipients, err)
	}
	newcomer := joinAs(t, s, "codex", "synthetic-decision-newcomer", later, "rx")
	if newcomer.Alias != address {
		t.Fatalf("address %q, want %q", newcomer.Alias, address)
	}
	mine, err = s.DecisionList(ctx, Key{"codex", newcomer.ID}, "mine", 0, 0)
	if err != nil || len(mine.Items) != 1 || mine.Items[0].Reference != "P-6" {
		t.Fatalf("mine after registration: %+v %v", mine, err)
	}
	waiting, err := s.DecisionList(ctx, callerHolder, "waiting", 0, 0)
	if err != nil || len(waiting.Items) != 5 {
		t.Fatalf("waiting excludes fyi: %+v %v", waiting, err)
	}
	// A stored command that fails the rule on read is hidden.
	if _, err := fixtureExec(s, `UPDATE decision_items SET commands=? WHERE reference='P-1'`, `["ls -l","rm​ -rf"]`); err != nil {
		t.Fatal(err)
	}
	got, err = s.DecisionGet(ctx, callerHolder, "P-1")
	if err != nil || len(got.Commands) != 1 || got.HiddenCommands != 1 {
		t.Fatalf("hidden command: %+v %v", got, err)
	}
}

// A walk in item ID order returns no item twice while items are edited and created.
func TestDecisionListWalkUnderChange(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	session := join(t, s, "codex", "synthetic-decision-walk", "")
	caller := Key{"codex", session.ID}
	for i := 1; i <= 5; i++ {
		f := decisionFields(fmt.Sprintf("Item %d", i), "decision")
		f.Order = ptr(int64(10 - i))
		mustCreate(t, s, caller, fmt.Sprintf("W-%d", i), f)
	}
	seen := map[string]int{}
	var after int64
	for step := 0; ; step++ {
		page, err := s.DecisionList(ctx, caller, "waiting", after, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			seen[item.Reference]++
		}
		if step == 0 {
			// Moving a walked item first and adding an item change no later page.
			if _, err := s.DecisionEdit(ctx, caller, "W-1", 1, DecisionFields{Order: ptr(int64(-5))}); err != nil {
				t.Fatal(err)
			}
			mustCreate(t, s, caller, "W-6", decisionFields("Late", "decision"))
		}
		if !page.More {
			break
		}
		after = *page.NextAfter
	}
	for i := 1; i <= 5; i++ {
		if seen[fmt.Sprintf("W-%d", i)] != 1 {
			t.Fatalf("walk: %v", seen)
		}
	}
	if seen["W-6"] > 1 {
		t.Fatalf("new item twice: %v", seen)
	}
}

// Each list reads one index range: the page cost does not grow with the stored items.
func TestDecisionListBoundedAtScale(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	session := join(t, s, "codex", "synthetic-decision-scale", "")
	caller := Key{"codex", session.ID}
	// Titles at their limit, in the character with the longest JSON escape.
	title := strings.Repeat("<", decisionTitleLimit)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	states := []string{"active", "in_progress", "archived"}
	for i := 1; i <= 10000; i++ {
		if _, err := tx.Exec(`INSERT INTO decision_items(reference,item_order,title,location,agent_text,context,recommendation,kind,state,commands,revision,
			author_family,author_name,created_at,updated_at) VALUES (?,?,?,'','','','',?,?,'[]',1,'codex','synthetic',1,1)`,
			fmt.Sprintf("S-%d", i), i, title, []string{"decision", "fyi"}[i%2], states[i%3]); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO decision_recipients(item_id,ordinal,name,kind,reason) VALUES (?,0,?,'peer','')`, i, session.Name); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, filter := range []string{"waiting", "active", "in_progress", "archived", "mine"} {
		page, err := s.DecisionList(ctx, caller, filter, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		reply, _ := json.Marshal(map[string]any{"ok": true, "result": page})
		if len(page.Items) == 0 || len(page.Items) > decisionPageItems || len(reply) > decisionPageBytes || !page.More {
			t.Fatalf("%s: %d items, %d bytes, more %v", filter, len(page.Items), len(reply), page.More)
		}
		if strings.Contains(string(reply), "<html") || strings.Contains(string(reply), "<table") {
			t.Fatalf("%s reply holds markup", filter)
		}
	}
	plans := map[string]string{
		"waiting": `SELECT id FROM decision_items WHERE state='active' AND kind='decision' AND id>0 ORDER BY id LIMIT 101`,
		"state":   `SELECT id FROM decision_items WHERE state='archived' AND id>0 ORDER BY id LIMIT 101`,
		"mine":    `SELECT DISTINCT item_id FROM decision_recipients WHERE name='x' AND item_id>0 ORDER BY item_id LIMIT 101`,
	}
	for name, query := range plans {
		rows, err := s.db.Query(`EXPLAIN QUERY PLAN ` + query)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		joined := strings.Join(plan, "; ")
		if !strings.Contains(joined, "USING") || strings.Contains(joined, "TEMP B-TREE") {
			t.Fatalf("%s plan: %s", name, joined)
		}
	}
	// A single summary at its largest serializes far below the page limit.
	largest, _ := json.Marshal(DecisionSummary{Reference: strings.Repeat("a", 32), Title: title, Kind: "decision", State: "in_progress", Revision: 1 << 62, Order: -1 << 62})
	if len(largest) >= decisionPageBytes/16 {
		t.Fatalf("summary %d bytes", len(largest))
	}
}

func TestDecisionReadsMoveNoCursor(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	session := join(t, s, "codex", "synthetic-decision-reader", "")
	sender := join(t, s, "claude", "synthetic-decision-sender", "")
	caller := Key{"codex", session.ID}
	if _, err := s.Send(ctx, Key{"claude", sender.ID}, session.Name, "synthetic unread message"); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, caller, "C-1", decisionFields("Cursor", "decision"))
	state := func() string {
		var b strings.Builder
		b.WriteString(rowsOf(t, s.db))
		rows, err := s.db.Query(`SELECT id,delivery_state,delivery_reason FROM messages ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id int64
			var state, reason string
			rows.Scan(&id, &state, &reason)
			fmt.Fprintln(&b, id, state, reason)
		}
		rows.Close()
		return b.String()
	}
	before := state()
	for _, filter := range []string{"waiting", "active", "mine"} {
		if _, err := s.DecisionList(ctx, caller, filter, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DecisionGet(ctx, caller, "C-1"); err != nil {
		t.Fatal(err)
	}
	if state() != before {
		t.Fatal("a decision read moved an inbox or memory cursor")
	}
}

func TestDecisionRoutes(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	if _, err := d.store.Register(context.Background(), Registration{Family: "deepseek", ID: "synthetic-dsh-decision", Directory: t.TempDir(), TTLSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	caller := Key{"deepseek", "synthetic-dsh-decision"}
	create := map[string]any{"caller": caller, "reference": "H-1", "title": "Route <b>item</b>", "kind": "decision",
		"context": "Use <script>x</script>.", "commands": []string{"make check"}, "key": "route-key", "deadline": d.store.clock() + 600}
	status, result := post(t, address, secret, "/v1/decisions/create", create)
	if status != 200 || result["result"].(map[string]any)["revision"] != 1.0 {
		t.Fatalf("create: %d %v", status, result)
	}
	status, result = post(t, address, secret, "/v1/decisions/edit", map[string]any{"caller": caller, "reference": "H-1", "if_revision": 1, "order": 3})
	if status != 200 || result["result"].(map[string]any)["revision"] != 2.0 {
		t.Fatalf("edit: %d %v", status, result)
	}
	status, result = post(t, address, secret, "/v1/decisions/get", map[string]any{"caller": caller, "reference": "H-1"})
	if status != 200 || result["result"].(map[string]any)["title"] != "Route <b>item</b>" {
		t.Fatalf("get: %d %v", status, result)
	}
	status, result = post(t, address, secret, "/v1/decisions/list", map[string]any{"caller": caller, "filter": "waiting"})
	if status != 200 || len(result["result"].(map[string]any)["items"].([]any)) != 1 {
		t.Fatalf("list: %d %v", status, result)
	}
	for label, c := range map[string]struct {
		path   string
		body   map[string]any
		status int
		code   string
	}{
		"guessed":     {"/v1/decisions/list", map[string]any{"caller": Key{"deepseek", "synthetic-guessed"}, "filter": "waiting"}, 403, "caller_inactive"},
		"maintainer":  {"/v1/decisions/get", map[string]any{"caller": maintainerKey, "reference": "H-1"}, 400, "invalid_request"},
		"verdict":     {"/v1/decisions/edit", map[string]any{"caller": caller, "reference": "H-1", "if_revision": 2, "verdict": "approved"}, 400, "invalid_request"},
		"dismissed":   {"/v1/decisions/edit", map[string]any{"caller": caller, "reference": "H-1", "if_revision": 2, "dismissed": true}, 400, "invalid_request"},
		"state":       {"/v1/decisions/edit", map[string]any{"caller": caller, "reference": "H-1", "if_revision": 2, "state": "archived"}, 400, "invalid_request"},
		"audit":       {"/v1/decisions/create", map[string]any{"caller": caller, "reference": "H-2", "title": "t", "kind": "fyi", "key": "k2", "deadline": d.store.clock() + 600, "audit": "x"}, 400, "invalid_request"},
		"agent row":   {"/v1/decisions/list", map[string]any{"caller": caller, "filter": "waiting", "activity": "idle"}, 400, "invalid_request"},
		"no deadline": {"/v1/decisions/create", map[string]any{"caller": caller, "reference": "H-2", "title": "t", "kind": "fyi", "key": "k2"}, 400, "invalid_request"},
		"no revision": {"/v1/decisions/edit", map[string]any{"caller": caller, "reference": "H-1", "title": "x"}, 400, "invalid_request"},
	} {
		if status, result := post(t, address, secret, c.path, c.body); status != c.status || result["code"] != c.code {
			t.Errorf("%s: %d %v", label, status, result)
		}
	}
}

// Schema 25 adds three empty tables and changes no retained message, session, cursor,
// claim or name.
func TestDecisionSchemaUpgradeKeepsRetainedState(t *testing.T) {
	s, root := testStore(t)
	ctx := context.Background()
	repo := testRepo(t)
	holder := joinAs(t, s, "codex", "synthetic-schema25-holder", repo, "")
	sender := join(t, s, "claude", "synthetic-schema25-sender", "")
	if _, err := s.Send(ctx, Key{"claude", sender.ID}, holder.Alias, "synthetic retained schema24 message"); err != nil {
		t.Fatal(err)
	}
	m := MemoryCaller{Family: "codex", Repository: "/synthetic/schema25/.git", Consumer: "synthetic-schema25", Name: "synthetic-schema25"}
	note(t, s, m, MemoryRecordRequest{Type: "finding", Body: "synthetic retained schema24 note"})
	before := rowsOf(t, s.db)
	if _, err := fixtureExec(s, undoSchemaTwentySeven+undoSchemaTwentySix+undoSchemaTwentyFive+`PRAGMA user_version=24`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(s.db, 24); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.db.Close()
	if count(t, upgraded, `PRAGMA user_version`) != schemaVersion || rowsOf(t, upgraded.db) != before {
		t.Fatal("schema25 changed retained state")
	}
	for _, table := range []string{"decision_items", "decision_recipients", "decision_replays"} {
		if count(t, upgraded, `SELECT COUNT(*) FROM `+table) != 0 {
			t.Fatalf("%s not empty", table)
		}
	}
	if err := checkCatalog(upgraded.db, 24); err == nil {
		t.Fatal("a schema24 reader accepted the schema25 store")
	}
}

// The read-only Decisions page shows agent text escaped and finds an item by reference.
func TestDecisionDashboardViewEscapesText(t *testing.T) {
	d, root := startTestDaemon(t)
	client := newActionClient(t, d, root)
	session := join(t, d.store, "codex", "synthetic-decision-page", "")
	caller := Key{"codex", session.ID}
	mustCreate(t, d.store, caller, "V-1", DecisionFields{Title: ptr("<script>synthetic title</script>"), Kind: ptr("decision"),
		Context: ptr("<img src=x onerror=alert(1)>"), Commands: &[]string{"echo '<b>'"}, Recipients: &[]string{"synthetic-nobody"}})
	page := dashboardDo(t, d, "GET", "/dashboard/decisions", client.cookie, nil, nil)
	if page.status != 200 || strings.Contains(page.body, "<script>synthetic") || !strings.Contains(page.body, "&lt;script&gt;synthetic title") ||
		!strings.Contains(page.body, "?ref=V-1") {
		t.Fatalf("list: %d %s", page.status, page.body)
	}
	page = dashboardDo(t, d, "GET", "/dashboard/decisions?ref=V-1", client.cookie, nil, nil)
	if page.status != 200 || strings.Contains(page.body, "<img src=x") || !strings.Contains(page.body, "&lt;img src=x") ||
		!strings.Contains(page.body, "not resolved: unknown_name") || !strings.Contains(page.body, "echo &#39;&lt;b&gt;&#39;") {
		t.Fatalf("item: %d %s", page.status, page.body)
	}
	for ref, text := range map[string]string{"V-9": "no decision item has this reference", "bad ref": "not a valid reference"} {
		page = dashboardDo(t, d, "GET", "/dashboard/decisions?ref="+url.QueryEscape(ref), client.cookie, nil, nil)
		if page.status != 200 || !strings.Contains(page.body, text) {
			t.Fatalf("%s: %d %s", ref, page.status, page.body)
		}
	}
	if page := dashboardDo(t, d, "GET", "/dashboard/decisions", "", nil, nil); page.status != 401 {
		t.Fatalf("without a session: %d", page.status)
	}
}
