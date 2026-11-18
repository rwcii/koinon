package core

import (
	"bytes"
	"errors"
	"fmt"
	"os"
)

// The read-only side of the skill entries (#741): what koinon install would do at each entry
// file, without a write, so that `koinon install --plan` can report a missing or earlier entry.
// Each state is the result that the matching install would report: "kept" (a linked entry,
// a Shared entry that is not a Koinon entry, or an OpenCode command without an intact marker),
// "unchanged", "created" or "replaced". A skill entry that is not a bounded regular file, such as
// a directory or a FIFO at the entry's path, or a path that cannot be inspected, is an error
// (skill_entry_unreadable), never a current plan; the size bound is the read's, not the install's.
// An OpenCode command file that is not a small regular file is not Koinon's, and the install
// keeps it, so its state is "kept"; only a command path that cannot be inspected is an error.

// skillEntryLimit bounds an entry read; Koinon's entries are a few hundred bytes.
const skillEntryLimit = 1 << 20

// readSkillEntry reads the entry at path without blocking: absent is (nil, false, nil).
func readSkillEntry(path string) ([]byte, bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, unreadableEntry(path, err)
	}
	data, err := readBounded(path, skillEntryLimit)
	if err != nil {
		return nil, true, unreadableEntry(path, err)
	}
	return data, true, nil
}

// unreadableEntry is the plan's coded error for an entry file that it could not inspect.
func unreadableEntry(path string, err error) error {
	return fmt.Errorf("skill_entry_unreadable: %s: %v", path, err)
}

// skillEntryState compares existing bytes with content as InstallSkillFile does.
func skillEntryState(data []byte, exists bool, content []byte) string {
	switch {
	case !exists:
		return "created"
	case bytes.Equal(data, content):
		return "unchanged"
	}
	return "replaced"
}

// SkillFileState reports what InstallSkillFile would do at path, without writing.
func SkillFileState(path string, content []byte) (string, error) {
	if linkedEntry(path) {
		return "kept", nil
	}
	data, exists, err := readSkillEntry(path)
	if err != nil {
		return "", err
	}
	return skillEntryState(data, exists, content), nil
}

// TeamSkillState reports what InstallTeamSkill would do at path, without writing.
func TeamSkillState(path string, skill TeamSkill) (string, error) {
	if linkedEntry(path) {
		return "kept", nil
	}
	data, exists, err := readSkillEntry(path)
	if err != nil {
		return "", err
	}
	if skill.Shared && exists && !bytes.Equal(data, skill.Content) && !bytes.Contains(data, []byte(teamSkillMark)) {
		return "kept", nil
	}
	return skillEntryState(data, exists, skill.Content), nil
}

// OpenCodeCommandState reports what InstallOpenCodeCommand would do at path, without writing.
// As there, a command file that is not a small regular file is not Koinon's, and is kept; a
// path that cannot be inspected is skill_entry_unreadable.
func OpenCodeCommandState(path string, command OpenCodeCommand) (string, error) {
	if linkedEntry(path) {
		return "kept", nil
	}
	data, exists, err := readOpenCodeCommand(path)
	if err != nil {
		return "", unreadableEntry(path, err)
	}
	if exists && !bytes.Equal(data, command.Content) && !openCodeCommandIntact(data) {
		return "kept", nil
	}
	return skillEntryState(data, exists, command.Content), nil
}
