package core

import (
	"context"
	"strings"
	"testing"
)

// Synthetic structure-only Codex screens of a narrow pane, where the status line takes two
// rows (#761). Each is the banner, the given composer rows, a blank row and the given footer.
func narrowScreen(composer []string, footer ...string) string {
	rows := append([]string{"", "  >_ OpenAI Codex (v0.162.0)", ""}, composer...)
	return strings.Join(append(append(rows, ""), footer...), "\n") + "\n"
}

const (
	narrowPlaceholder = "\x1b[1m›\x1b[0m \x1b[2mAsk Codex to do anything\x1b[0m"
	narrowStatus      = "  synthetic-model · /tmp/synthetic/repo · Context 64% used · weekly"
	narrowStatusTail  = "  69% left"
	narrowHint        = "  ← for agents · ? for shortcuts            ⚠ 3 warnings · f2 to view"
)

// A footer of 3 or 4 rows is found when its last row is the shortcut hint; any other run of
// 3 or more rows, or a footer with no blank row above it, leaves the composer not empty.
func TestCodexNarrowFooter(t *testing.T) {
	idle := narrowScreen([]string{narrowPlaceholder}, narrowStatus, narrowStatusTail, narrowHint)
	c := readCodexScreen(idle)
	if i := c.composer(); c.footer(i) != i+2 || !c.empty(i) || !codexIdle(idle) || codexScreenBusy(c) != "" {
		t.Fatalf("narrow idle screen: footer %d, composer %d, idle %v", c.footer(i), i, codexIdle(idle))
	}
	four := narrowScreen([]string{narrowPlaceholder}, narrowStatus, narrowStatusTail, "  synthetic third row", narrowHint)
	if !codexIdle(four) {
		t.Fatal("4-row footer that ends with the hint")
	}

	for name, screen := range map[string]string{
		"draft on the mark row":          narrowScreen([]string{"\x1b[1m›\x1b[0m synthetic draft"}, narrowStatus, narrowStatusTail),
		"draft, hint left on the screen": narrowScreen([]string{"\x1b[1m›\x1b[0m synthetic draft"}, narrowStatus, narrowStatusTail, narrowHint),
		"draft after a blank first row":  narrowScreen([]string{"\x1b[1m›\x1b[0m ", "", "  synthetic second line"}, narrowStatus, narrowStatusTail),
		"3 rows without the hint":        narrowScreen([]string{narrowPlaceholder}, narrowStatus, narrowStatusTail, "  synthetic row"),
		"hint not the last of 3 rows":    narrowScreen([]string{narrowPlaceholder}, narrowStatus, narrowHint, narrowStatusTail),
		"hint not the last of 4 rows":    narrowScreen([]string{narrowPlaceholder}, narrowHint, narrowStatus, narrowStatusTail, "  synthetic row"),
		"5 rows that end with the hint":  narrowScreen([]string{narrowPlaceholder}, narrowStatus, narrowStatusTail, "  synthetic row", "  synthetic row", narrowHint),
		"no blank row above the footer":  "\n  >_ OpenAI Codex (v0.162.0)\n\n" + strings.Join([]string{narrowPlaceholder, narrowStatus, narrowStatusTail, narrowHint}, "\n") + "\n",
	} {
		if codexIdle(screen) {
			t.Errorf("%s: read as an empty composer", name)
		}
	}

	// A typed notice wraps in the composer, and the hint is gone: the readback is the notice.
	notice := codexTestNotice
	typed := narrowScreen([]string{"\x1b[1m›\x1b[0m " + notice[:60], "  " + notice[60:]}, narrowStatus, narrowStatusTail)
	if got := codexDraft(typed); squeeze(got) != squeeze(notice) {
		t.Fatalf("readback %q", got)
	}
}

// In a narrow pane, a waiting compaction's busy check gives "" for an idle pane, whose Inspect
// then proves the foreground and reports it idle; a draft keeps it waiting and sends no key.
func TestCodexNarrowCompaction(t *testing.T) {
	ctx := context.Background()
	term := &busyTerminal{codexTerminal: newCodexTerminal(t), inMode: "0\n"}
	term.narrow = true
	target := term.target(t)
	if got := (codexCycle{}).CompactBusy(ctx, target); got != "" {
		t.Fatalf("busy check of the idle narrow pane: %q", got)
	}
	if pane, err := (codexCycle{}).Inspect(ctx, target, term.fence); err != nil || !pane.Idle || term.groups != 1 {
		t.Fatalf("Inspect: %+v %v after %d table reads", pane, err, term.groups)
	}
	term.input = "draft"
	if got := (codexCycle{}).CompactBusy(ctx, target); got != "input_not_empty" || len(term.sent()) != 0 {
		t.Fatalf("draft: %q, keys %q", got, term.sent())
	}
}

// With startup warnings, Codex CLI 0.172.0 keeps the warnings mark alone on the last footer
// row while the composer holds text (#825). A footer of 3 or 4 rows that ends with that mark
// is the footer; the same mark after other text on the row, or 5 rows, is not.
func TestCodexWarningsFooter(t *testing.T) {
	const mark = "                              ⚠  2 warnings · f2 to view"
	notice := codexTestNotice
	typed := narrowScreen([]string{"\x1b[1m›\x1b[0m " + notice[:60], "  " + notice[60:]}, narrowStatus, narrowStatusTail, mark)
	if got := codexDraft(typed); squeeze(got) != squeeze(notice) {
		t.Fatalf("readback %q", got)
	}
	if one := narrowScreen([]string{"\x1b[1m›\x1b[0m " + notice}, narrowStatus, narrowStatusTail, "  ⚠ 1 warning · f2 to view"); squeeze(codexDraft(one)) != squeeze(notice) {
		t.Fatal("one warning")
	}
	for name, screen := range map[string]string{
		"draft with text before the mark": narrowScreen([]string{narrowPlaceholder}, narrowStatus, narrowStatusTail, "  synthetic "+strings.TrimSpace(mark)),
		"mark not the last of 3 rows":     narrowScreen([]string{narrowPlaceholder}, narrowStatus, mark, narrowStatusTail),
		"5 rows that end with the mark":   narrowScreen([]string{narrowPlaceholder}, narrowStatus, narrowStatusTail, "  synthetic row", "  synthetic row", mark),
	} {
		if codexIdle(screen) {
			t.Errorf("%s: read as an empty composer", name)
		}
	}
}

// The pane wake types the notice into an idle pane whose footer ends with the warnings mark
// alone, reads it back and submits it with Enter (#825).
func TestCodexWakeWarningsPane(t *testing.T) {
	f := newCodexFixture(t)
	p := f.pane("warnings", "sh -c 'PROGRAM; exit'")
	session := f.session(f.child(p.shell), p.id, "idle")
	if got := f.s.providerWake(context.Background(), session, codexTestNotice); got != accepted() {
		t.Fatalf("wake: %v\n%s", got, f.run("capture-pane", "-p", "-t", p.id))
	}
	if got := f.received(p); got != codexTestNotice+"\n" {
		t.Fatalf("received %q", got)
	}
}

// The pane wake types the notice into an idle narrow pane and reads it back before Enter.
func TestCodexWakeNarrowPane(t *testing.T) {
	f := newCodexFixture(t)
	p := f.pane("narrow", "sh -c 'PROGRAM; exit'")
	session := f.session(f.child(p.shell), p.id, "idle")
	if got := f.s.providerWake(context.Background(), session, codexTestNotice); got != accepted() {
		t.Fatalf("wake: %v\n%s", got, f.run("capture-pane", "-p", "-t", p.id))
	}
	if got := f.received(p); got != codexTestNotice+"\n" {
		t.Fatalf("received %q", got)
	}
}
