package core

import (
	"os"
	"path/filepath"
	"testing"
)

// The retired Python blocks leave the Codex AGENTS.md; every other byte stays (#487).
func TestRemoveRetiredCodexBlocks(t *testing.T) {
	koinon := "\n<!-- BEGIN KOINON CODEX -->\nretired text\n<!-- END KOINON CODEX -->\n"
	bridge := "\n<!-- BEGIN CODEX PEER BRIDGE -->\nolder text\n<!-- END CODEX PEER BRIDGE -->\n"
	for _, c := range []struct{ name, in, want string }{
		{"block only", koinon, ""},
		{"block at the start", "<!-- BEGIN KOINON CODEX -->\nretired\n<!-- END KOINON CODEX -->\nkept\n", "kept\n"},
		{"text before", "# Mine\nkeep this\n" + koinon, "# Mine\nkeep this\n"},
		{"text before and after", "before\n" + koinon + "after\n", "before\nafter\n"},
		{"both blocks", "a\n" + bridge + "b\n" + koinon, "a\nb\n"},
		{"repeated", koinon + "middle\n" + koinon, "middle\n"},
		{"CRLF text", "one\r\ntwo\r\n" + koinon + "three\r\n", "one\r\ntwo\r\nthree\r\n"},
		{"CRLF block", "one\r\n\r\n<!-- BEGIN KOINON CODEX -->\r\nx\r\n<!-- END KOINON CODEX -->\r\ntwo\r\n", "one\r\ntwo\r\n"},
		{"no block", "# Mine\nonly mine\n", "# Mine\nonly mine\n"},
		{"begin without end", "a\n<!-- BEGIN KOINON CODEX -->\nb\nc\n", "a\n<!-- BEGIN KOINON CODEX -->\nb\nc\n"},
		{"end before begin", "<!-- END KOINON CODEX -->\na\n" + koinon, "<!-- END KOINON CODEX -->\na\n"},
		{"marker inside a line", "see <!-- BEGIN KOINON CODEX --> here\n<!-- END KOINON CODEX -->\n", "see <!-- BEGIN KOINON CODEX --> here\n<!-- END KOINON CODEX -->\n"},
		{"other pair's end", "a\n<!-- BEGIN KOINON CODEX -->\nb\n<!-- END CODEX PEER BRIDGE -->\nc\n", "a\n<!-- BEGIN KOINON CODEX -->\nb\n<!-- END CODEX PEER BRIDGE -->\nc\n"},
		{"no final newline", "a\n\n<!-- BEGIN KOINON CODEX -->\nb\n<!-- END KOINON CODEX -->", "a\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := string(withoutRetiredBlocks([]byte(c.in))); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
	dir := t.TempDir()
	path := CodexInstructions(dir, "/unused")
	if result, err := RemoveRetiredCodexBlocks(path); result != "absent" || err != nil {
		t.Fatalf("absent file: %s %v", result, err)
	}
	os.WriteFile(path, []byte("mine\n"+koinon), 0o640)
	if result, err := RemoveRetiredCodexBlocks(path); result != "removed" || err != nil {
		t.Fatalf("remove: %s %v", result, err)
	}
	data, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(data) != "mine\n" || info.Mode().Perm() != 0o640 {
		t.Fatalf("after: %q %v", data, info.Mode())
	}
	if result, err := RemoveRetiredCodexBlocks(path); result != "unchanged" || err != nil {
		t.Fatalf("again: %s %v", result, err)
	}
	// A linked file keeps its link; its target loses the block.
	target := filepath.Join(dir, "real.md")
	os.WriteFile(target, []byte(koinon+"kept\n"), 0o600)
	link := filepath.Join(dir, "linked", "AGENTS.md")
	os.MkdirAll(filepath.Dir(link), 0o700)
	os.Symlink(target, link)
	if result, err := RemoveRetiredCodexBlocks(link); result != "removed" || err != nil {
		t.Fatalf("link: %s %v", result, err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced")
	}
	if data, _ := os.ReadFile(target); string(data) != "kept\n" {
		t.Fatalf("target: %q", data)
	}
	if CodexInstructions("", "/home/x") != "/home/x/.codex/AGENTS.md" || CodexInstructions("/c", "/home/x") != "/c/AGENTS.md" {
		t.Fatal("CodexInstructions path")
	}
}
