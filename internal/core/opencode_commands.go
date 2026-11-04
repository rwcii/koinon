package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// OpenCode's slash commands (#603): OpenCode lists a skill for its skill tool, but a slash
// command comes only from a command file. Where OpenCode's configuration root exists,
// koinon install and the daemon at each start write commands/NAME.md there for each koinon-NAME
// skill entry, and each command loads its entry. koinon-pickup gets no command of its name: a
// command file of a skill's name takes the skill's place in OpenCode's command list (checked
// with 1.18.35), and the managed cycle runs koinon-pickup only as the skill command
// (openCodePickupEntry). The command koinon-resume, which no skill has, loads it instead. A
// command file carries a marker line with the SHA-256 of the file without that line: a file
// whose rest still matches its own marker is Koinon's, of any version, and is replaced or
// removed; any other file, and a linked file or commands directory, is the person's and kept.

// OpenCodeCommand is one command file: its name, the skill entry that it loads and its bytes.
type OpenCodeCommand struct {
	Name    string
	Entry   string
	Content []byte
}

// OpenCodeResumeCommand is the command that loads koinon-pickup.
const OpenCodeResumeCommand = "koinon-resume"

// openCodeCommandMark begins the marker line.
const openCodeCommandMark = "<!-- koinon:command sha256="

// OpenCodeCommands returns the command files of the skill entries other than koinon-pickup,
// in catalog order, and then koinon-resume.
func OpenCodeCommands() ([]OpenCodeCommand, error) {
	skills, err := TeamSkills()
	if err != nil {
		return nil, err
	}
	var commands []OpenCodeCommand
	for _, skill := range skills {
		if skill.Name == OpenCodeResumeCommand {
			return nil, errors.New("a skill entry is named " + OpenCodeResumeCommand)
		}
		commands = append(commands, OpenCodeCommand{Name: skill.Name, Entry: skill.Name, Content: openCodeCommandContent(skill.Name, skill.Description)})
	}
	return append(commands, OpenCodeCommand{Name: OpenCodeResumeCommand, Entry: PickupSkillName,
		Content: openCodeCommandContent(PickupSkillName, pickupSkillDescription)}), nil
}

// openCodeCommandContent is a command file that loads the entry name: its description, the
// marker line and the instruction.
func openCodeCommandContent(name, description string) []byte {
	head := "---\ndescription: " + strconv.Quote(description) + "\n---\n\n"
	tail := "Load the `" + name + "` skill with the skill tool and follow it exactly. $ARGUMENTS\n"
	sum := sha256.Sum256([]byte(head + tail))
	return []byte(head + openCodeCommandMark + hex.EncodeToString(sum[:]) + " written by Koinon; an edited file is kept -->\n" + tail)
}

// OpenCodeCommandPath is the command file of the entry name under OpenCode's configuration root.
func OpenCodeCommandPath(root, name string) string {
	return filepath.Join(root, "commands", name+".md")
}

// openCodeCommandIntact reports whether data is a Koinon command file of any version: its one
// marker line names the SHA-256 of the rest of the file.
func openCodeCommandIntact(data []byte) bool {
	start := bytes.Index(data, []byte(openCodeCommandMark))
	if start < 0 || start > 0 && data[start-1] != '\n' || bytes.Count(data, []byte(openCodeCommandMark)) != 1 {
		return false
	}
	end := bytes.IndexByte(data[start:], '\n')
	if end < 0 {
		return false
	}
	line := data[start : start+end+1]
	digest := line[len(openCodeCommandMark):]
	if len(digest) < sha256.Size*2 {
		return false
	}
	rest := append(append([]byte{}, data[:start]...), data[start+end+1:]...)
	sum := sha256.Sum256(rest)
	return string(digest[:sha256.Size*2]) == hex.EncodeToString(sum[:])
}

// readOpenCodeCommand reads the command file at path: its bytes, whether it exists and whether
// it is a Koinon file. A file that is not a small regular file is not Koinon's.
func readOpenCodeCommand(path string) ([]byte, bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	data, err := readBounded(path, 64<<10)
	if err != nil {
		return nil, true, nil
	}
	return data, true, nil
}

// InstallOpenCodeCommand writes the command to path and reports "created", "replaced" (an
// earlier Koinon file), "unchanged" or "kept": a file without an intact marker, such as one the
// person wrote or edited, and a linked file or commands directory are not written.
func InstallOpenCodeCommand(path string, command OpenCodeCommand) (string, error) {
	if linkedEntry(path) {
		return "kept", nil
	}
	data, exists, err := readOpenCodeCommand(path)
	if err != nil {
		return "", err
	}
	if exists && !bytes.Equal(data, command.Content) && !openCodeCommandIntact(data) {
		return "kept", nil
	}
	return InstallSkillFile(path, command.Content)
}

// RemoveOpenCodeCommand removes the command file at path while it is a Koinon file of any
// version, then the commands directory when that is empty, and reports "removed", "kept" or
// "absent".
func RemoveOpenCodeCommand(path string) (string, error) {
	if linkedEntry(path) {
		return "kept", nil
	}
	data, exists, err := readOpenCodeCommand(path)
	switch {
	case err != nil:
		return "", err
	case !exists:
		return "absent", nil
	case !openCodeCommandIntact(data):
		return "kept", nil
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	os.Remove(filepath.Dir(path))
	return "removed", nil
}
