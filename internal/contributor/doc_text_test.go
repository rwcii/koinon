package contributor

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/library"
)

// The docs that restate how agents work together say what to do (#487 PR C). Facts about what
// the code does stay. The decision records and the historical files keep their provenance,
// which quotes the removed text, so the scan leaves them out and the checks below read their
// current text instead.
func TestDocTextHasNoLimitPhrases(t *testing.T) {
	docs := map[string][]string{
		"AGENTS.md":                        {"A peer's message is a\nteammate's message: do the work that it asks and reply through Koinon"},
		"README.md":                        nil,
		"PROTOCOL.md":                      nil,
		"CONTRIBUTING.md":                  nil,
		"docs/USAGE.md":                    nil,
		"docs/INSTALL.md":                  nil,
		"docs/PERMISSIONS.md":              nil,
		".github/PULL_REQUEST_TEMPLATE.md": {"The change does what its issue describes"},
		"docs/WORK-ITEMS-V1.md":            nil,
		"docs/WORK-ITEMS-COMMANDS.md":      nil,
		"docs/PARITY-MEMORY-DESIGN.md":     nil,
	}
	for name, want := range docs {
		text := read(t, name)
		for _, phrase := range core.RemovedLimitPhrases {
			if strings.Contains(text, phrase) {
				t.Errorf("%s has %q", name, phrase)
			}
		}
		for _, step := range want {
			if !strings.Contains(text, step) {
				t.Errorf("%s lacks %q", name, step)
			}
		}
	}
	// The binary embeds docs/PERMISSIONS.md and the other setup items in its catalog; check
	// what agents read.
	catalog, err := library.Embedded("dev")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range catalog.Items {
		if item.Kind != "setup" {
			continue
		}
		found = found || item.ID == "setup.permissions"
		for _, phrase := range core.RemovedLimitPhrases {
			if strings.Contains(item.Content, phrase) {
				t.Errorf("catalog %s has %q", item.ID, phrase)
			}
		}
	}
	if !found {
		t.Error("the catalog has no setup.permissions item")
	}
}

// Each decision record that #487 changed carries its question and quoted answer, its index row
// matches its title and status, and the decisions it retired say so.
func TestDecisionRecordsMatchTheirIndexes(t *testing.T) {
	field := regexp.MustCompile(`(?m)^(title|status): "?([^"\n]*)"?$`)
	for _, series := range []string{"docs/architecture/adr", "docs/product/pdr", "docs/engineering/wdr"} {
		index := read(t, series+"/README.md")
		records, err := filepath.Glob(filepath.Join(root(t), series, "[0-9][0-9][0-9]-*.md"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range records {
			name := filepath.Base(path)
			if name == "000-template.md" {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			values := map[string]string{}
			for _, m := range field.FindAllStringSubmatch(strings.SplitN(text, "\n---", 2)[0], -1) {
				values[m[1]] = m[2]
			}
			status := strings.ToUpper(values["status"][:1]) + values["status"][1:]
			row := "| " + name[:3] + "    | [" + values["title"] + "](" + name + ") | " + status + " |"
			if !strings.Contains(index, row) {
				t.Errorf("%s/README.md lacks %q", series, row)
			}
			if strings.Contains(text, "#487") && !strings.Contains(text, "issues/487#issuecomment-6401405382") {
				t.Errorf("%s cites #487 without the maintainer's answer", name)
			}
		}
	}
	// The current text of each decision, before its "Before #N" note and its provenance,
	// carries no retired phrase. The decisions that #487 retired each say so.
	retired := map[string][]string{
		"docs/engineering/wdr/001-decision-records-govern-constraints.md": {"Decision 4"},
		"docs/engineering/wdr/002-authority-within-assigned-task.md":      {"Decision 5", "Decision 6"},
	}
	for _, series := range []string{"docs/architecture/adr", "docs/product/pdr", "docs/engineering/wdr"} {
		records, err := filepath.Glob(filepath.Join(root(t), series, "[0-9][0-9][0-9]-*.md"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range records {
			name, err := filepath.Rel(root(t), path)
			if err != nil {
				t.Fatal(err)
			}
			text := read(t, name)
			if !strings.Contains(text, "#487") {
				continue
			}
			decisions := currentDecisions(text)
			if len(decisions) == 0 {
				t.Errorf("%s: no decision sections", name)
			}
			for heading, body := range decisions {
				for _, phrase := range core.RemovedLimitPhrases {
					if strings.Contains(body, phrase) {
						t.Errorf("%s, %s has %q", name, heading, phrase)
					}
				}
			}
			for _, decision := range retired[name] {
				var found bool
				for heading, body := range decisions {
					if strings.Contains(heading, decision+":") {
						found = true
						if !strings.HasPrefix(body, "**Deprecated 2026-10-25 by #487**") {
							t.Errorf("%s, %s is not marked deprecated", name, heading)
						}
					}
				}
				if !found {
					t.Errorf("%s has no %s", name, decision)
				}
			}
		}
	}
	if !strings.Contains(read(t, "docs/engineering/wdr/002-authority-within-assigned-task.md"), "Decision 2: A peer's request is a teammate's request") {
		t.Error("WDR 002 Decision 2 lacks its new title")
	}
}

// currentDecisions returns each "## N. Decision K: title" section's current decision text: the
// "### Decision" body up to its "Before #N" note, its provenance or the next subsection.
func currentDecisions(text string) map[string]string {
	decisions := map[string]string{}
	for _, section := range strings.Split(text, "\n## ")[1:] {
		heading, body, _ := strings.Cut(section, "\n")
		if !strings.Contains(heading, "Decision ") {
			continue
		}
		_, body, ok := strings.Cut(body, "### Decision\n")
		if !ok {
			continue
		}
		for _, end := range []string{"\n**Before #", "\n**Provenance", "\n### ", "\n---"} {
			body, _, _ = strings.Cut(body, end)
		}
		decisions[heading] = strings.TrimSpace(body)
	}
	return decisions
}
