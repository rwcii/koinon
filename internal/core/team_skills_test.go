package core

import (
	"strconv"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/library"
)

// Each team skill entry loads its catalog item and names the item's own repository facts
// file; it holds no procedure and no project data, so one entry serves every repository, with
// or without a sidecar (#453).
func TestTeamSkills(t *testing.T) {
	skills, err := TeamSkills()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := library.Embedded("")
	if err != nil {
		t.Fatal(err)
	}
	items := map[string]library.Item{}
	for _, item := range catalog.Items {
		items[item.Name] = item
	}
	var names []string
	for _, skill := range skills {
		names = append(names, skill.Name)
		name := strings.TrimPrefix(skill.Name, "koinon-")
		item, ok := items[name]
		if !ok || item.Facts == nil {
			t.Fatalf("%s: no catalog item with facts", skill.Name)
		}
		content := string(skill.Content)
		for _, want := range []string{
			"---\nname: " + skill.Name + "\ndescription: " + strconv.Quote(item.Description) + "\n---\n",
			"library_read tool for item skill." + name + " and follow the returned procedure",
			"from " + item.Facts.Filename + " as its Repository facts section says",
		} {
			if !strings.Contains(content, want) {
				t.Fatalf("%s lacks %q:\n%s", skill.Name, want, content)
			}
		}
		if len(content) > 1024 || strings.Contains(content, "\n## ") {
			t.Fatalf("%s holds a procedure:\n%s", skill.Name, content)
		}
	}
	if got := strings.Join(names, " "); got != "koinon-spin-up koinon-spin-down koinon-recover-team koinon-peer-tmux koinon-handoff" {
		t.Fatalf("entries: %s", got)
	}
}
