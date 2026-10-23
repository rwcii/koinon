package core

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rwcii/koinon/internal/library"
)

// The skill entries (#453, #461): koinon install, and the daemon at each start, write a
// Koinon-owned entry named koinon-NAME for each catalog skill, for the person who runs the
// daemon and never into a repository, at the same places as koinon-pickup. An entry holds no
// procedure: it loads its catalog item, whose procedure reads the repository's own facts from
// .koinon/skills/NAME.md. The pickup skill's entry is koinon-pickup itself. A skill whose name
// already starts with koinon-, such as koinon-guide, keeps its name; because that name is also
// the catalog item's own, which an adopted copy may hold, its entry replaces only a Koinon entry.

// TeamSkill is one skill entry: its name and its file's bytes.
type TeamSkill struct {
	Name    string
	Content []byte
	// Shared: the name is also the catalog item's own (#461).
	Shared bool
}

// teamSkillMark begins every entry's body; a file without it is not a Koinon entry.
const teamSkillMark = "\n---\n\nCall the koinon library_read tool for item skill."

// InstallTeamSkill writes the entry to path as InstallSkillFile does. A Shared entry keeps an
// existing file that is not a Koinon entry, such as an adopted copy of its item ("kept").
func InstallTeamSkill(path string, skill TeamSkill) (string, error) {
	if skill.Shared && !linkedEntry(path) {
		data, err := os.ReadFile(path)
		if err == nil && !bytes.Equal(data, skill.Content) && !bytes.Contains(data, []byte(teamSkillMark)) {
			return "kept", nil
		}
	}
	return InstallSkillFile(path, skill.Content)
}

// TeamSkills returns the skill entries other than koinon-pickup, one for each skill of the
// embedded catalog, in catalog order; each carries its item's description so that the command
// menus list it.
func TeamSkills() ([]TeamSkill, error) {
	catalog, err := library.Embedded("")
	if err != nil {
		return nil, err
	}
	var skills []TeamSkill
	for _, item := range catalog.Items {
		if item.Kind != "skill" || item.Name == "pickup" {
			continue
		}
		if item.Facts == nil {
			return nil, fmt.Errorf("catalog skill.%s declares no repository facts", item.Name)
		}
		name, shared := "koinon-"+item.Name, strings.HasPrefix(item.Name, "koinon-")
		if shared {
			name = item.Name
		}
		skills = append(skills, TeamSkill{Name: name, Content: []byte(teamSkillContent(name, item.Name, item.Facts.Filename, item.Description)), Shared: shared})
	}
	if len(skills) == 0 {
		return nil, fmt.Errorf("catalog has no skills")
	}
	return skills, nil
}

func teamSkillContent(name, item, facts, description string) string {
	return "---\nname: " + name + "\ndescription: " + strconv.Quote(description) + "\n---\n\n" +
		"Call the koinon library_read tool for item skill." + item + " and follow the returned procedure. It reads\n" +
		"this repository's facts from " + facts + " as its Repository facts section says. If the\n" +
		"tool is unavailable, stop and report that.\n"
}

// SkillRoot is one family's user skill root, which holds skills/NAME/SKILL.md (#461).
type SkillRoot struct {
	Family string
	Root   string
	// IfPresent: the family gets its entries only when Root already exists, so that no
	// directory is made for an agent that is not on the computer.
	IfPresent bool
}

// Path is the file of the entry name under the root.
func (s SkillRoot) Path(name string) string {
	return filepath.Join(s.Root, "skills", name, "SKILL.md")
}

// Present reports whether the family gets its entries.
func (s SkillRoot) Present() bool {
	if !s.IfPresent {
		return true
	}
	info, err := os.Stat(s.Root)
	return err == nil && info.IsDir()
}

// SkillRoots are the user skill roots whose discovery was verified (#461, docs/INSTALL.md):
// Claude Code's configuration root, Codex's ~/.agents in home, and OpenCode's and agy's
// configuration directories, as koinon setup uses them. Grok and DeepSeek have none.
func SkillRoots(claudeConfig, home, opencode, agy string) []SkillRoot {
	return []SkillRoot{
		{Family: "claude", Root: claudeConfig},
		{Family: "codex", Root: filepath.Join(home, ".agents")},
		{Family: "opencode", Root: opencode, IfPresent: true},
		{Family: "agy", Root: agy, IfPresent: true},
	}
}

// OpenCodeConfigRoot is OpenCode's global configuration directory: xdgConfig/opencode when
// $XDG_CONFIG_HOME is set, else ~/.config/opencode in home.
func OpenCodeConfigRoot(xdgConfig, home string) string {
	if xdgConfig == "" {
		xdgConfig = filepath.Join(home, ".config")
	}
	return filepath.Join(xdgConfig, "opencode")
}

// AgyConfigRoot is agy's global customization root, ~/.gemini/config in home.
func AgyConfigRoot(home string) string { return filepath.Join(home, ".gemini", "config") }
