package core

import (
	"fmt"
	"strconv"

	"github.com/rwcii/koinon/internal/library"
)

// The team skills (#453): koinon install, and the daemon at each start, write a Koinon-owned
// entry named koinon-NAME for each of these catalog skills, for the person who runs the
// daemon and never into a repository, at the same places as koinon-pickup. An entry holds no
// procedure: it loads its catalog item, whose procedure reads the repository's own facts from
// .koinon/skills/NAME.md. The pickup skill's entry is koinon-pickup itself.
var teamSkillItems = []string{"spin-up", "spin-down", "recover-team", "peer-tmux", "handoff"}

// TeamSkill is one team skill entry: its name and its file's bytes.
type TeamSkill struct {
	Name    string
	Content []byte
}

// TeamSkills returns the team skill entries, built from the embedded catalog's items, whose
// descriptions they carry so that the command menus list them.
func TeamSkills() ([]TeamSkill, error) {
	catalog, err := library.Embedded("")
	if err != nil {
		return nil, err
	}
	descriptions := map[string]string{}
	for _, item := range catalog.Items {
		if item.Kind == "skill" {
			descriptions[item.Name] = item.Description
		}
	}
	skills := make([]TeamSkill, 0, len(teamSkillItems))
	for _, name := range teamSkillItems {
		description, ok := descriptions[name]
		if !ok {
			return nil, fmt.Errorf("catalog has no skill.%s", name)
		}
		skills = append(skills, TeamSkill{Name: "koinon-" + name, Content: []byte(teamSkillContent(name, description))})
	}
	return skills, nil
}

func teamSkillContent(name, description string) string {
	return "---\nname: koinon-" + name + "\ndescription: " + strconv.Quote(description) + "\n---\n\n" +
		"Call the koinon library_read tool for item skill." + name + " and follow the returned procedure. It reads\n" +
		"this repository's facts from .koinon/skills/" + name + ".md as its Repository facts section says. If the\n" +
		"tool is unavailable, stop and report that.\n"
}
