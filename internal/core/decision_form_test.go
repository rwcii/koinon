package core

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const goodCardContext = "- One fact. Source: a file, 2026-11-07.\n  It continues here.\n\n- Another fact. Source: #1, 2026-11-07.\n1. A numbered fact.\n"

const goodCardRecommendation = "## Recommendation: Do the thing\n\n**What:**\n1. A step.\n\n**Why:** The defect.\n\n" +
	"**Fast or complete:** Complete.\n\n**Corrections from the peer:** One.\n\n**Open disagreements:** none\n\n" +
	"**Other options:**\n- Another.\n\n**A separate choice, which you can add to any option:** One.\n"

func notesHave(notes []string, part string) bool {
	for _, note := range notes {
		if strings.Contains(note, part) {
			return true
		}
	}
	return false
}

func TestCardFormNotes(t *testing.T) {
	if notes := CardFormNotes("decision", goodCardContext, goodCardRecommendation); notes != nil {
		t.Fatalf("a card in the form has notes: %q", notes)
	}
	minimal := "## Recommendation: x\n**What:** a\n**Why:** b\n**Fast or complete:** c"
	if notes := CardFormNotes("decision", "- a", minimal); notes != nil {
		t.Fatalf("the optional labels are optional: %q", notes)
	}
	// An indented list and an indented continuation are a list, as the dashboard renders them.
	for _, indent := range []string{" ", "  ", "   "} {
		context := indent + "- A fact.\n" + indent + "  It continues.\n\tSo does this.\n" + indent + "2. Another fact."
		if rendered := string(renderMarkdown(context)); !strings.Contains(rendered, "<li>") {
			t.Fatalf("not rendered as a list: %q", rendered)
		}
		if notes := CardFormNotes("decision", context, minimal); notes != nil {
			t.Fatalf("%d-space list: %q", len(indent), notes)
		}
	}
	for _, kind := range []string{"task", "fyi"} {
		if notes := CardFormNotes(kind, "prose", "prose"); notes != nil {
			t.Fatalf("%s has notes: %q", kind, notes)
		}
	}
	cases := []struct {
		name, context, recommendation, want string
	}{
		{"prose context", "A paragraph.\n", minimal, "list of facts"},
		{"prose after a list", "- a\nA paragraph.\n", minimal, "list of facts"},
		{"empty context", "", minimal, "list of facts"},
		{"blank context", " \n\n", minimal, "list of facts"},
		{"code block, not a list", "    - a\n", minimal, "list of facts"},
		{"indented text first", "  text\n- a\n", minimal, "list of facts"},
		{"no heading", "- a", "Recommendation: x\n**What:** a\n**Why:** b\n**Fast or complete:** c", "## Recommendation:"},
		{"heading not first", "- a", "Text.\n## Recommendation: x\n**What:** a\n**Why:** b\n**Fast or complete:** c", "## Recommendation:"},
		{"no What", "- a", "## Recommendation: x\n**Why:** b\n**Fast or complete:** c", "add **What:**"},
		{"no Why", "- a", "## Recommendation: x\n**What:** a\n**Fast or complete:** c", "add **Why:**"},
		{"no Fast", "- a", "## Recommendation: x\n**What:** a\n**Why:** b", "add **Fast or complete:**"},
		{"a label inside a sentence", "- a", "## Recommendation: x\nSee **What:** a\n**Why:** b\n**Fast or complete:** c", "add **What:**"},
		{"required out of order", "- a", "## Recommendation: x\n**Why:** b\n**What:** a\n**Fast or complete:** c", "order"},
		{"optional before required", "- a", "## Recommendation: x\n**Other options:** o\n**What:** a\n**Why:** b\n**Fast or complete:** c", "order"},
		{"optional out of order", "- a", minimal + "\n**Other options:** o\n**Open disagreements:** d", "order"},
		{"long context", "- " + strings.Repeat("a", cardContextRunes), minimal, "2502 characters"},
		{"long recommendation", "- a", minimal + "\n" + strings.Repeat("a", cardRecommendationRunes), "characters; keep it to 3000"},
	}
	for _, c := range cases {
		if notes := CardFormNotes("decision", c.context, c.recommendation); !notesHave(notes, c.want) {
			t.Errorf("%s: notes %q, want %q", c.name, notes, c.want)
		}
	}
}

// The sizes count characters, not bytes, and the size itself passes.
func TestCardFormNotesSizes(t *testing.T) {
	minimal := "## Recommendation: x\n**What:** a\n**Why:** b\n**Fast or complete:** c\n"
	context := "- " + strings.Repeat("é", cardContextRunes-2)
	if notes := CardFormNotes("decision", context, minimal); notes != nil {
		t.Fatalf("context at the size: %q", notes)
	}
	if notes := CardFormNotes("decision", context+"é", minimal); !notesHave(notes, "2501 characters") {
		t.Fatalf("context over the size: %q", notes)
	}
	recommendation := minimal + strings.Repeat("é", cardRecommendationRunes-len(minimal))
	if notes := CardFormNotes("decision", "- a", recommendation); notes != nil {
		t.Fatalf("recommendation at the size: %q", notes)
	}
	if notes := CardFormNotes("decision", "- a", recommendation+"é"); !notesHave(notes, "3001 characters") {
		t.Fatalf("recommendation over the size: %q", notes)
	}
}

// The text that an author reads says the sizes that the daemon checks.
func TestCardFormTextNamesTheSizes(t *testing.T) {
	if !strings.Contains(CardContextForm, "at most "+strconv.Itoa(cardContextRunes)+" characters") {
		t.Fatalf("context form: %q", CardContextForm)
	}
	if !strings.Contains(CardRecommendationForm, "at most "+strconv.Itoa(cardRecommendationRunes)+" characters") {
		t.Fatalf("recommendation form: %q", CardRecommendationForm)
	}
	for _, label := range append(append([]string{}, cardRequired...), cardOptional...) {
		if !strings.Contains(strings.ReplaceAll(CardRecommendationForm, "\n", " "), label) {
			t.Fatalf("recommendation form has no %s", label)
		}
	}
}

// A create reply notes the created text, a replay repeats those notes after the card was
// repaired, and an edit notes the merged item. A note refuses nothing and changes no state.
func TestDecisionFormNotesInReplies(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	if _, err := d.store.Register(context.Background(), Registration{Family: "deepseek", ID: "synthetic-dsh-form", Directory: t.TempDir(), TTLSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	caller := Key{"deepseek", "synthetic-dsh-form"}
	body := map[string]any{"caller": caller, "reference": "FORM-1", "title": "Synthetic", "kind": "decision",
		"context": "A paragraph.", "recommendation": "Prose.", "key": "form-key", "deadline": d.store.clock() + 600}
	notes := func(result map[string]any) []any {
		list, _ := result["result"].(map[string]any)["form_notes"].([]any)
		return list
	}
	status, created := post(t, address, secret, "/v1/decisions/create", body)
	first := notes(created)
	if status != 200 || len(first) != 5 || created["result"].(map[string]any)["state"] != "active" {
		t.Fatalf("create: %d %v", status, created)
	}
	status, edited := post(t, address, secret, "/v1/decisions/edit", map[string]any{"caller": caller, "reference": "FORM-1", "if_revision": 1,
		"context": goodCardContext})
	if status != 200 || len(notes(edited)) != 4 || edited["result"].(map[string]any)["revision"] != 2.0 {
		t.Fatalf("partial edit checks the merged item: %d %v", status, edited)
	}
	status, edited = post(t, address, secret, "/v1/decisions/edit", map[string]any{"caller": caller, "reference": "FORM-1", "if_revision": 2,
		"recommendation": goodCardRecommendation})
	if _, present := edited["result"].(map[string]any)["form_notes"]; status != 200 || present {
		t.Fatalf("a repaired card has no notes: %d %v", status, edited)
	}
	status, replayed := post(t, address, secret, "/v1/decisions/create", body)
	result := replayed["result"].(map[string]any)
	if status != 200 || result["duplicate"] != true || result["revision"] != 1.0 || !reflect.DeepEqual(notes(replayed), first) {
		t.Fatalf("replay: %d %v", status, replayed)
	}
	status, got := post(t, address, secret, "/v1/decisions/get", map[string]any{"caller": caller, "reference": "FORM-1"})
	item := got["result"].(map[string]any)
	if _, present := item["form_notes"]; status != 200 || present || item["revision"] != 3.0 || item["state"] != "active" {
		t.Fatalf("get after the replay: %d %v", status, got)
	}
	status, task := post(t, address, secret, "/v1/decisions/create", map[string]any{"caller": caller, "reference": "FORM-2", "title": "Synthetic",
		"kind": "task", "context": "A paragraph.", "key": "form-task", "deadline": d.store.clock() + 600})
	if _, present := task["result"].(map[string]any)["form_notes"]; status != 200 || present {
		t.Fatalf("task: %d %v", status, task)
	}
}
