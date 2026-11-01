package core

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/library"
)

// Each skill entry loads its catalog item and names the item's own repository facts file; it
// holds no procedure and no project data, so one entry serves every repository, with or
// without a sidecar (#453). Every catalog skill but pickup has one, and a skill already named
// koinon-NAME, such as koinon-guide, keeps its name as a Shared entry (#461).
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
	var want []string
	for _, item := range catalog.Items {
		if item.Kind != "skill" {
			continue
		}
		items[item.Name] = item
		if item.Name != "pickup" {
			want = append(want, "koinon-"+strings.TrimPrefix(item.Name, "koinon-"))
		}
	}
	var names []string
	for _, skill := range skills {
		names = append(names, skill.Name)
		name := strings.TrimPrefix(skill.Name, "koinon-")
		if _, ok := items[skill.Name]; ok {
			name = skill.Name
		}
		if skill.Shared != (name == skill.Name) {
			t.Fatalf("%s: shared %v", skill.Name, skill.Shared)
		}
		item, ok := items[name]
		if !ok || item.Facts == nil {
			t.Fatalf("%s: no catalog item with facts", skill.Name)
		}
		content := string(skill.Content)
		for _, want := range []string{
			"---\nname: " + skill.Name + "\ndescription: " + strconv.Quote(item.Description) + "\n---\n",
			"library_read tool for item skill." + name + " and follow the returned procedure",
			"from " + item.Facts.Filename + " as its Repository facts section says",
			"If the\ntool is unavailable, stop and report that.\n", // DECISIONS.md#RESTORE-STEPS-487
		} {
			if !strings.Contains(content, want) {
				t.Fatalf("%s lacks %q:\n%s", skill.Name, want, content)
			}
		}
		if len(content) > 1024 || strings.Contains(content, "\n## ") {
			t.Fatalf("%s holds a procedure:\n%s", skill.Name, content)
		}
	}
	if len(want) != 13 || strings.Join(names, " ") != strings.Join(want, " ") {
		t.Fatalf("entries %q, want %q", names, want)
	}
	for _, name := range []string{"koinon-guide", "koinon-check", "koinon-spin-up", "koinon-handoff", "koinon-sprint"} {
		if !slices.Contains(names, name) {
			t.Fatalf("no entry %s in %q", name, names)
		}
	}
	if slices.Contains(names, "koinon-pickup") || slices.Contains(names, "koinon-koinon-guide") {
		t.Fatalf("entries %q", names)
	}
}
