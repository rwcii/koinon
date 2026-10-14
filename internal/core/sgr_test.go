package core

import "testing"

// The arguments of an extended color are color data, never the dim attribute (#371).
func TestSGRDim(t *testing.T) {
	for _, c := range []struct {
		params    string
		from, dim bool
	}{
		{"2", false, true},
		{"0", true, false},
		{"", true, false},
		{"22", true, false},
		{"1;2", false, true},
		{"0;2", false, true},
		// A truecolor background or foreground: the 2 selects the 24-bit form.
		{"48;2;30;68;78", false, false},
		{"48;2;30;68;78", true, true},
		{"38;2;2;2;2", false, false},
		{"38;2;0;22;0", true, true},
		// A 256-color index of 0, 2 or 22.
		{"38;5;2", false, false},
		{"48;5;22", true, true},
		{"58;5;0", true, true},
		// Attributes around a color still count.
		{"2;48;2;30;68;78", false, true},
		{"48;2;30;68;78;22", true, false},
		{"38;5;2;2", false, true},
		// A truncated color reads what is there; a colon form is one parameter.
		{"38;2;1", false, false},
		{"38;5", true, true},
		{"48", false, false},
		{"38:2::1:2:3", false, false},
	} {
		if got := sgrDim(c.params, c.from); got != c.dim {
			t.Errorf("sgrDim(%q, %v) = %v, want %v", c.params, c.from, got, c.dim)
		}
	}
}

func TestSGRReadersTruecolor(t *testing.T) {
	// The composer text after the mark, on a 24-bit background, as Codex CLI 0.162.1 drew it on
	// macOS.
	typed := "\x1b[0m\x1b[48;2;30;68;78m synthetic draft"
	if _, undimmed := sgrText(typed); !undimmed {
		t.Error("a truecolor draft read as dim")
	}
	placeholder := "\x1b[0m\x1b[48;2;30;68;78m \x1b[2mAsk Codex to do anything"
	if _, undimmed := sgrText(placeholder); undimmed {
		t.Error("a dim placeholder on a truecolor background read as typed")
	}
	if _, undimmed := sgrText("\x1b[2m\x1b[38;5;2mAsk Codex"); undimmed {
		t.Error("a 256-color index ended the dim attribute")
	}
	for line, want := range map[string]string{
		"\x1b[48;2;30;68;78m/clear":                        "/clear",
		"\x1b[38;5;2mReply":                                "Reply",
		"\x1b[2mTry \"fix\"\x1b[22m\x1b[38;2;2;2;2m typed": " typed",
		"\x1b[2m\x1b[38;5;0msuggestion":                    "",
	} {
		if got := visibleText(line); got != want {
			t.Errorf("visibleText(%q) = %q, want %q", line, got, want)
		}
	}
	if s := parseClaudeScreen("\x1b[0m" + claudeInputMark + "\x1b[48;2;30;68;78mdraft text\x1b[0m\n"); !s.found || s.input != "draft text" {
		t.Errorf("truecolor Claude input: found %v input %q", s.found, s.input)
	}
}
