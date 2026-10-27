package core

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
)

// The managed cycle's pickup entry (#406): a Koinon-owned skill named koinon-pickup, which no
// project uses. koinon install writes it for the person who runs the daemon, never into a
// repository: skills/koinon-pickup/SKILL.md under the Claude Code configuration root, and
// ~/.agents/skills/koinon-pickup/SKILL.md for Codex. After the clear the cycle enters
// /koinon-pickup (Claude Code) or $koinon-pickup (Codex). A project's own pickup skill takes
// no part in the cycle.

// PickupSkillName is the name of the cycle's pickup skill and of its directory.
const PickupSkillName = "koinon-pickup"

// pickupSkillDescription is the skill's description, which Claude Code's command menu lists.
const pickupSkillDescription = "Koinon's managed-cycle entry for the canonical pickup procedure. Resume work from this agent's newest handoff."

// pickupSkillContent is the skill's bytes. Its body only reads the canonical catalog item and
// checks it against the cycle's pinned catalog digest; it holds no procedure of its own, so it
// changes only when this entry changes.
const pickupSkillContent = `---
name: ` + PickupSkillName + `
description: ` + pickupSkillDescription + `
---

Call the koinon library_read tool for item skill.pickup and follow the returned procedure.
`

// PickupSkill returns the skill's bytes.
func PickupSkill() []byte { return []byte(pickupSkillContent) }

// deepSeekPickupContent is DeepSeek's koinon-pickup entry (#514). DeepSeek has the command
// path, not the MCP tools, so its entry reads the same catalog item with koinon library read;
// the procedure stays the one item.
const deepSeekPickupContent = `---
name: ` + PickupSkillName + `
description: ` + pickupSkillDescription + `
---

Run koinon library read skill.pickup --as deepseek:$DSH_SESSION_ID and follow the returned procedure.
`

// DeepSeekPickupSkill returns DeepSeek's entry bytes.
func DeepSeekPickupSkill() []byte { return []byte(deepSeekPickupContent) }

func deepSeekPickupEntry() PickupEntry {
	return PickupEntry{Content: DeepSeekPickupSkill(), Description: pickupSkillDescription}
}

// DeepSeekHome is the DeepSeek Harness home: dshHome ($DSH_HOME) when it is set, else ~/.dsh.
func DeepSeekHome(dshHome, home string) string {
	if dshHome != "" {
		return dshHome
	}
	return filepath.Join(home, ".dsh")
}

// DeepSeekPickupSkillPath is the entry in the harness home's user skills, which the harness
// lists before ~/.agents/skills, where Codex's entry is (#514).
func DeepSeekPickupSkillPath(dshHome string) string {
	return filepath.Join(dshHome, "skills", PickupSkillName, "SKILL.md")
}

// pickupSkillEntry is the skill's bytes and its menu description.
func pickupSkillEntry() PickupEntry {
	return PickupEntry{Content: PickupSkill(), Description: pickupSkillDescription}
}

// ClaudeConfigRoot is a person's Claude Code configuration root: configDir (their
// CLAUDE_CONFIG_DIR) when it is set, else ~/.claude in home.
func ClaudeConfigRoot(configDir, home string) string {
	if configDir != "" {
		return configDir
	}
	return filepath.Join(home, ".claude")
}

// ClaudePickupSkillPath is the skill's file under a Claude Code configuration root
// ($CLAUDE_CONFIG_DIR, else ~/.claude).
func ClaudePickupSkillPath(configRoot string) string {
	return ClaudeSkillPath(configRoot, PickupSkillName)
}

// CodexPickupSkillPath is the skill's file in a home directory's user skills, which Codex lists
// for every session.
func CodexPickupSkillPath(home string) string { return CodexSkillPath(home, PickupSkillName) }

// ClaudeSkillPath is a personal skill's file under a Claude Code configuration root.
func ClaudeSkillPath(configRoot, name string) string {
	return filepath.Join(configRoot, "skills", name, "SKILL.md")
}

// CodexSkillPath is a user skill's file in a home directory's ~/.agents/skills.
func CodexSkillPath(home, name string) string {
	return filepath.Join(home, ".agents", "skills", name, "SKILL.md")
}

// pickupSkillReason checks the installed skill file against want: "" when it is byte-equal,
// else pickup_entry_missing, pickup_entry_unreadable or pickup_entry_differs.
func pickupSkillReason(path string, want []byte) string {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return "pickup_entry_missing"
	}
	data, err := readBounded(path, 1<<20)
	if err != nil {
		return "pickup_entry_unreadable"
	}
	if len(want) == 0 || !bytes.Equal(data, want) {
		return "pickup_entry_differs"
	}
	return ""
}

// pickupSkillStep names the command that installs the skill at path, for a reason of
// pickupSkillReason.
func pickupSkillStep(path string) string {
	return "run koinon install to install the " + PickupSkillName + " skill at " + path
}

// InstallPickupSkill writes the skill to path, replacing a different file, and reports
// "created", "replaced" or "unchanged". Missing directories are created private to this account.
func InstallPickupSkill(path string) (string, error) { return InstallSkillFile(path, PickupSkill()) }

// InstallSkillFile writes content to path, replacing a different file, and reports "created",
// "replaced" or "unchanged". Missing directories are created private to this account. An
// entry directory or file that is a symbolic link is the person's own, such as a link to a
// project's skill, and is "kept" without a write (#461).
func InstallSkillFile(path string, content []byte) (string, error) {
	if linkedEntry(path) {
		return "kept", nil
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil && bytes.Equal(data, content):
		return "unchanged", nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return "", err
	}
	result := "replaced"
	if err != nil {
		result = "created"
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".SKILL-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return result, nil
}

// RemovePickupSkill removes the skill at path while it is this binary's skill, then its
// directory when that is empty, and reports "removed", "kept" (another content) or "absent".
func RemovePickupSkill(path string) (string, error) { return RemoveSkillFile(path, PickupSkill()) }

// RemoveSkillFile removes the file at path while it holds content, then its directory when
// that is empty, and reports "removed", "kept" (another content, or a linked entry directory or
// file) or "absent".
func RemoveSkillFile(path string, content []byte) (string, error) {
	if linkedEntry(path) {
		return "kept", nil
	}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "absent", nil
	case err != nil:
		return "", err
	case !bytes.Equal(data, content):
		return "kept", nil
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	os.Remove(filepath.Dir(path))
	return "removed", nil
}

// linkedEntry reports whether the skill file at path, or its entry directory, is a symbolic
// link, which Koinon neither writes through nor removes.
func linkedEntry(path string) bool {
	for _, p := range []string{filepath.Dir(path), path} {
		if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}
