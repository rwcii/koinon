// Package library builds inert adoption recommendations from canonical embedded sources.
// It reads no runtime checkout, installs no files and edits no native configuration.
package library

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	skills "github.com/rwcii/koinon/agents/skills"
	reference "github.com/rwcii/koinon/docs"
	"github.com/rwcii/koinon/internal/capabilities"
	"github.com/rwcii/koinon/internal/skillmeta"
)

const MaxItems = 32
const MaxItemBytes = 64 << 10

// FactField declares a heading in a repository fact sidecar. Required fields have no
// portable default. Optional fields must declare their default in the canonical skill.
type FactField struct {
	Heading  string  `json:"heading"`
	Required bool    `json:"required"`
	Default  *string `json:"default,omitempty"`
}
type Facts struct {
	Version  int         `json:"version"`
	Filename string      `json:"filename"`
	Fields   []FactField `json:"fields"`
}

// Destination describes a potential target, not an authorized write. Root is a typed
// resolver name, never a resolved private home path. An unknown native root is omitted.
type Destination struct {
	Family    string `json:"family"`
	Scope     string `json:"scope"`
	Root      string `json:"root,omitempty"`
	Path      string `json:"path,omitempty"`
	Discovery string `json:"discovery"`
	Evidence  string `json:"evidence"`
}
type Item struct {
	ID                  string        `json:"id"`
	Kind                string        `json:"kind"`
	Name                string        `json:"name"`
	Description         string        `json:"description"`
	Purpose             string        `json:"purpose"`
	RequiringRelease    string        `json:"requiring_release"`
	FeatureRequirements []string      `json:"feature_requirements"`
	Destinations        []Destination `json:"destinations"`
	Facts               *Facts        `json:"repository_facts,omitempty"`
	Content             string        `json:"content,omitempty"`
	ContentDigest       string        `json:"content_digest"`
}
type Catalog struct {
	Version int    `json:"version"`
	Items   []Item `json:"items"`
	Digest  string `json:"catalog_digest"`
}
type Link struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

// DiscoveryLinks is the shared project layout. Copies must not replace these links.
func DiscoveryLinks() []Link {
	return []Link{{".agents/skills", "../agents/skills"}, {".codex/skills", "../agents/skills"}, {".claude/skills", "../agents/skills"}}
}

type Command struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Embedded constructs the binary's complete catalog from its source whitelist.
func Embedded(release string) (Catalog, error) {
	return Build(skills.Source, reference.Permissions, release)
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func read(source fs.FS, name string) ([]byte, error) {
	f, err := source.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("catalog source must be a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxItemBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || len(b) > MaxItemBytes || !utf8.Valid(b) || bytes.ContainsRune(b, 0) {
		return nil, errors.New("invalid or oversized catalog source")
	}
	return b, nil
}

// ParseFacts reads a schema from the skill's sole explicit declaration. No independent
// table of fields/defaults exists. Repository fact values are never part of this catalog.
func ParseFacts(data []byte, name string) (*Facts, error) {
	const marker = "<!-- koinon:repository-facts -->\n```json\n"
	if bytes.Count(data, []byte(marker)) != 1 {
		return nil, errors.New("skill needs one repository-facts declaration")
	}
	_, tail, _ := bytes.Cut(data, []byte(marker))
	raw, rest, ok := bytes.Cut(tail, []byte("\n```"))
	if !ok || strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("repository-facts declaration must finish the skill")
	}
	var facts Facts
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&facts); err != nil {
		return nil, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("trailing schema data")
	}
	if facts.Version != 1 || facts.Filename != ".koinon/skills/"+name+".md" || facts.Fields == nil || len(facts.Fields) > 32 {
		return nil, errors.New("invalid repository-facts schema")
	}
	seen := map[string]bool{}
	for _, field := range facts.Fields {
		h := field.Heading
		if h == "" || h != strings.TrimSpace(h) || len(h) > 128 || strings.ContainsFunc(h, unicode.IsControl) || strings.ContainsAny(h, "#:`") || seen[h] || slices.Contains([]string{"Version", "Skill", "Sources"}, h) {
			return nil, errors.New("invalid or duplicate fact heading")
		}
		seen[h] = true
		if field.Required && field.Default != nil || !field.Required && (field.Default == nil || *field.Default == "" || len(*field.Default) > 1024 || strings.ContainsFunc(*field.Default, unicode.IsControl)) {
			return nil, errors.New("required fact has default or optional fact lacks bounded default")
		}
	}
	return &facts, nil
}

func destinations(name string, families []string) []Destination {
	result := []Destination{}
	for _, family := range families {
		result = append(result, Destination{Family: family, Scope: "project", Root: "worktree", Path: "agents/skills/" + name + "/SKILL.md", Discovery: "direct-read", Evidence: "canonical project file; native discovery is family-dependent"})
		machine := Destination{Family: family, Scope: "machine", Discovery: "unverified", Evidence: "native user skill root not verified; read-only recommendation"}
		switch family {
		case "codex":
			machine.Root = "codex-home"
			machine.Path = "skills/" + name + "/SKILL.md"
			machine.Discovery = "verified"
			machine.Evidence = "Codex 0.162.0 isolated app-server skills/list; effective CODEX_HOME, default user .codex"
		case "claude":
			machine.Root = "claude-default-home"
			machine.Path = "skills/" + name + "/SKILL.md"
			machine.Evidence = "documented personal .claude/skills; native discovery and nondefault config root unverified; read-only"
		}
		result = append(result, machine)
	}
	return result
}
func projectDestinations(target string, families []string) []Destination {
	result := []Destination{}
	for _, family := range families {
		result = append(result, Destination{Family: family, Scope: "project", Root: "worktree", Path: target, Discovery: "reference", Evidence: "explicit project-local proposal; no native configuration edit"})
	}
	return result
}

// Build also supports a bounded synthetic source filesystem for source-completeness tests.
// All directories must contain valid skill metadata/schema; none is silently omitted.
func Build(source, permissions fs.FS, release string) (Catalog, error) {
	if release == "" || release == "dev" || !strings.HasPrefix(release, "v") {
		release = "development"
	}
	if len(release) > 128 || strings.ContainsFunc(release, unicode.IsControl) {
		return Catalog{}, errors.New("invalid catalog release")
	}
	families := capabilities.Families()
	features := capabilities.Features()
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		return Catalog{}, err
	}
	cat := Catalog{Version: 1, Items: []Item{}}
	commands := []Command{}
	add := func(item Item) error {
		if len(cat.Items) >= MaxItems || len(item.Content) == 0 || len(item.Content) > MaxItemBytes || !utf8.ValidString(item.Content) {
			return errors.New("catalog bound exceeded")
		}
		for _, prior := range cat.Items {
			if prior.ID == item.ID {
				return errors.New("duplicate catalog ID")
			}
		}
		for _, feature := range item.FeatureRequirements {
			if !slices.Contains(features, feature) {
				return fmt.Errorf("unimplemented feature %s", feature)
			}
		}
		item.RequiringRelease = release
		item.ContentDigest = digest([]byte(item.Content))
		cat.Items = append(cat.Items, item)
		return nil
	}
	for _, entry := range entries {
		if entry.Type()&fs.ModeSymlink != 0 {
			return Catalog{}, errors.New("canonical source cannot be a symlink")
		}
		if !entry.IsDir() {
			continue
		}
		b, err := read(source, path.Join(entry.Name(), "SKILL.md"))
		if err != nil {
			return Catalog{}, err
		}
		name, desc, err := skillmeta.Metadata(b, entry.Name())
		if err != nil {
			return Catalog{}, err
		}
		facts, err := ParseFacts(b, name)
		if err != nil {
			return Catalog{}, fmt.Errorf("%s: %w", name, err)
		}
		requirements := []string{}
		if name == "pair" || name == "peer-review" {
			requirements = []string{"peer-messaging", "work-claims"}
		}
		if err := add(Item{ID: "skill." + name, Kind: "skill", Name: name, Description: desc, Purpose: desc, FeatureRequirements: requirements, Destinations: destinations(name, families), Facts: facts, Content: string(b)}); err != nil {
			return Catalog{}, err
		}
		commands = append(commands, Command{Path: ".opencode/commands/" + name + ".md", Content: string(skillmeta.CommandWrapper(name, desc))})
	}
	if len(commands) == 0 {
		return Catalog{}, errors.New("empty canonical skills source")
	}
	authoring, err := read(source, "AGENTS.md")
	if err != nil {
		return Catalog{}, err
	}
	perms, err := read(permissions, "PERMISSIONS.md")
	if err != nil {
		return Catalog{}, err
	}
	links, _ := json.Marshal(DiscoveryLinks())
	wrappers, _ := json.Marshal(struct {
		Ownership string    `json:"ownership"`
		Commands  []Command `json:"commands"`
	}{"Generated ownership marker; preserve custom commands and refuse collisions or symlinks. Adopt only the nine unchanged original templates; removal affects only owned wrappers.", commands})
	guidance := []byte("<!-- koinon:catalog-guidance -->\nRead the installed `koinon guide --agent FAMILY` within the task. Inspect the installed catalog before proposing skill adoption. Each proposed skill or setup adoption requires exact-change approval (PDR 008); permission recommendations are inert.\n<!-- /koinon:catalog-guidance -->\n")
	for _, setup := range []struct {
		id, target, purpose string
		content             []byte
	}{
		{"shared-instructions", "agents/skills/AGENTS.md", "Portable canonical skill authoring guidance", authoring},
		{"discovery-links", "agents/skills", "Literal shared discovery link descriptors", links},
		{"opencode-commands", ".opencode/commands", "Generated command descriptors and ownership rules", wrappers},
		{"guidance", "AGENTS.md", "Inert marked project guide and catalog reference", guidance},
		{"permissions", "docs/koinon/PERMISSIONS.md", "Inert native permission recommendation reference", perms},
	} {
		if err := add(Item{ID: "setup." + setup.id, Kind: "setup", Name: setup.id, Description: setup.purpose, Purpose: setup.purpose, FeatureRequirements: []string{}, Destinations: projectDestinations(setup.target, families), Content: string(setup.content)}); err != nil {
			return Catalog{}, err
		}
	}
	slices.SortFunc(cat.Items, func(a, b Item) int { return strings.Compare(a.ID, b.ID) })
	canonical, err := json.Marshal(struct {
		Version int    `json:"version"`
		Items   []Item `json:"items"`
	}{cat.Version, cat.Items})
	if err != nil {
		return Catalog{}, err
	}
	cat.Digest = digest(canonical)
	return cat, nil
}
