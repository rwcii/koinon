package contributor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The newest-handoff line of the pickup and handoff skills gives the same file in every locale
// (#737): two handoffs of the same second, one with a label, sort in byte order.
func TestNewestHandoffLineIgnoresTheLocale(t *testing.T) {
	locales := []string{"C"}
	if out, err := exec.Command("locale", "-a").Output(); err == nil {
		for _, l := range strings.Fields(string(out)) {
			if l == "en_US.UTF-8" || l == "en_US.utf8" {
				locales = append(locales, l)
				break
			}
		}
	}
	if len(locales) == 1 {
		t.Log("en_US.UTF-8 is not installed; only the C locale is checked")
	}
	for skill, variable := range map[string]string{"pickup": "file", "handoff": "last"} {
		text, err := os.ReadFile(filepath.Join(root(t), "agents/skills", skill, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		var line string
		for _, l := range strings.Split(string(text), "\n") {
			if strings.HasPrefix(l, variable+"=$(ls \"$dir\"/") {
				line = l
			}
		}
		if line == "" {
			t.Fatalf("%s: no %s=$(ls ...) line", skill, variable)
		}
		dir := t.TempDir()
		for _, name := range []string{"2026-09-28-190500.md", "2026-09-28-190500-x.md", "2026-09-28-185959-z.md", "progress.md"} {
			if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for _, locale := range locales {
			cmd := exec.Command("sh", "-c", line+"\nprintf '%s' \"$"+variable+"\"")
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "dir=" + dir, "LANG=" + locale, "LC_ALL=" + locale}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s in %s: %v\n%s", skill, locale, err, out)
			}
			if got := filepath.Base(string(out)); got != "2026-09-28-190500.md" {
				t.Errorf("%s in %s picks %s", skill, locale, got)
			}
		}
	}
}
