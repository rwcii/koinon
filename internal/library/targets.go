package library

import (
	"encoding/json"
	"errors"
	"path"
	"strings"
)

// Target is derived from the embedded catalog, never from a receipt or tool argument.
// Its path and expected bytes stay inside the inspecting client.
type Target struct {
	ID       string
	Path     string
	Kind     string
	Expected []byte
}

var ErrUnsupportedScope = errors.New("catalog item does not support this scope")

func safeCatalogPath(name string) bool {
	return name != "" && name != "." && !path.IsAbs(name) && path.Clean(name) == name &&
		name != ".." && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\\x00\r\n")
}

// Targets returns at most 32 declared targets within the selected root. Machine targets
// without native discovery evidence are an inspection limitation, not absent files.
func (c Catalog) Targets(family, scope, id string) ([]Target, error) {
	read, err := c.Read(family, id)
	if err != nil {
		return nil, err
	}
	item := read.Item
	var destination *Destination
	for i := range item.Destinations {
		if item.Destinations[i].Scope == scope {
			destination = &item.Destinations[i]
			break
		}
	}
	if destination == nil {
		return nil, ErrUnsupportedScope
	}
	if scope == "machine" && (destination.Discovery != "verified" || destination.Root != "codex-home") {
		return nil, ErrUnsupportedScope
	}
	if !safeCatalogPath(destination.Path) {
		return nil, ErrInvalidQuery
	}
	if item.Kind == "skill" {
		return []Target{{"skill", destination.Path, "file", []byte(item.Content)}}, nil
	}
	switch item.ID {
	case "setup.discovery-links":
		var links []Link
		if json.Unmarshal([]byte(item.Content), &links) != nil || len(links) == 0 || len(links) > MaxTargets {
			return nil, ErrInvalidQuery
		}
		result := []Target{}
		for _, link := range links {
			var name string
			switch link.Path {
			case ".agents/skills":
				name = "link.agents"
			case ".codex/skills":
				name = "link.codex"
			case ".claude/skills":
				name = "link.claude"
			default:
				return nil, ErrInvalidQuery
			}
			if link.Target != "../agents/skills" {
				return nil, ErrInvalidQuery
			}
			result = append(result, Target{name, link.Path, "link", []byte(link.Target)})
		}
		return result, nil
	case "setup.opencode-commands":
		var collection struct {
			Ownership string    `json:"ownership"`
			Commands  []Command `json:"commands"`
		}
		if json.Unmarshal([]byte(item.Content), &collection) != nil || len(collection.Commands) == 0 || len(collection.Commands) > MaxTargets {
			return nil, ErrInvalidQuery
		}
		result := []Target{}
		seen := map[string]bool{}
		for _, command := range collection.Commands {
			name := strings.TrimSuffix(path.Base(command.Path), ".md")
			id := "command." + name
			if !targetPattern.MatchString(id) || command.Path != ".opencode/commands/"+name+".md" ||
				seen[id] || command.Content == "" || len(command.Content) > MaxItemBytes {
				return nil, ErrInvalidQuery
			}
			seen[id] = true
			result = append(result, Target{id, command.Path, "file", []byte(command.Content)})
		}
		return result, nil
	case "setup.guidance":
		return []Target{{"fragment", destination.Path, "fragment", []byte(item.Content)}}, nil
	case "setup.shared-instructions", "setup.permissions":
		return []Target{{"file", destination.Path, "file", []byte(item.Content)}}, nil
	}
	return nil, ErrInvalidQuery
}

// CheckTargets checks only descriptor IDs. Unknown recorded descriptors are inert and
// cannot become arbitrary filesystem reads when comparing old collection contents.
func CheckTargets(declared []Target, recorded []TargetDigest) error {
	allowed := map[string]bool{}
	for _, target := range declared {
		allowed[target.ID] = true
	}
	for _, target := range recorded {
		if !allowed[target.ID] {
			return ErrInvalidDecision
		}
	}
	return nil
}
