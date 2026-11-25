package library

import (
	"errors"
	"io/fs"
	"regexp"
	"strings"

	skills "github.com/rwcii/koinon/agents/skills"
)

// ErrNoStandardLens means the preflight skill has no standard lens of that name.
var ErrNoStandardLens = errors.New("the preflight skill has no standard lens of that name")

var lensName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// StandardLens returns the brief of the standard lens NAME: the text under "### NAME" in the
// "## Standard lenses" section of the preflight skill in source, as scripts/preflight-run.sh
// reads it (WDR 004, Decision 8; PDR 008, Decision 5; #814).
func StandardLens(source fs.FS, name string) (string, error) {
	if !lensName.MatchString(name) {
		return "", ErrNoStandardLens
	}
	b, err := fs.ReadFile(source, "preflight/SKILL.md")
	if err != nil {
		return "", err
	}
	var brief []string
	standard, on := false, false
	for _, line := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			standard, on = strings.TrimSpace(line) == "## Standard lenses", false
			continue
		case standard && strings.HasPrefix(line, "### "):
			fields := strings.Fields(line)
			on = len(fields) > 1 && fields[1] == name
			continue
		}
		if on {
			brief = append(brief, line)
		}
	}
	text := strings.TrimSpace(strings.Join(brief, "\n"))
	if text == "" {
		return "", ErrNoStandardLens
	}
	return text + "\n", nil
}

// EmbeddedStandardLens reads the standard lens NAME from the skills in this binary.
func EmbeddedStandardLens(name string) (string, error) { return StandardLens(skills.Source, name) }
