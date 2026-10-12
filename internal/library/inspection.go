package library

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// InspectionContext is filled by the native client after its registered repository and
// selected worktree have been verified. None of these paths is a model-facing argument.
type InspectionContext struct {
	Family      string
	Scope       string
	Repository  string
	ProjectRoot string
	MachineRoot string
}

type TargetState struct {
	ID     string `json:"id"`
	State  string `json:"state,omitempty"`
	Digest string `json:"digest,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type FactState struct {
	State        string    `json:"state"`
	Digest       string    `json:"digest,omitempty"`
	SchemaDigest string    `json:"schema_digest,omitempty"`
	Required     []string  `json:"required,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	Decision     *Decision `json:"decision,omitempty"`
	DecisionSeq  int64     `json:"decision_seq,omitempty"`
	DecisionNote string    `json:"decision_reason,omitempty"`
}

type ItemState struct {
	ItemID           string        `json:"item_id"`
	Scope            string        `json:"scope"`
	ScopeDigest      string        `json:"scope_digest,omitempty"`
	State            string        `json:"state,omitempty"`
	Reason           string        `json:"reason,omitempty"`
	Targets          []TargetState `json:"targets"`
	Facts            *FactState    `json:"repository_facts,omitempty"`
	Decision         *Decision     `json:"decision,omitempty"`
	DecisionNote     string        `json:"decision_reason,omitempty"`
	DecisionRevision int64         `json:"decision_revision,omitempty"`
	DecisionSeq      int64         `json:"decision_seq,omitempty"`
	Collision        string        `json:"name_collision,omitempty"`
	CollisionNote    string        `json:"collision_reason,omitempty"`
	Recommended      bool          `json:"recommended"`
}

// ScopeDigest identifies a verified root without returning its private path.
func ScopeDigest(scope, root string) string {
	data, _ := json.Marshal([2]string{scope, root})
	return digest(data)
}

type Inspector struct {
	// OpenRoot defaults to OpenReadRoot. Injection changes no native caller metadata.
	OpenRoot func(string) (ReadRoot, error)
}

func (i Inspector) open(root string) (ReadRoot, error) {
	if root == "" {
		return nil, fs.ErrNotExist
	}
	if i.OpenRoot != nil {
		return i.OpenRoot(root)
	}
	return OpenReadRoot(root)
}

func readReason(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "unreadable"
	case errors.Is(err, errNotRegular):
		return "non_regular_target"
	case errors.Is(err, ErrInvalidQuery):
		return "item_too_large"
	default:
		return "inspection_error"
	}
}

func baseline(decision *Decision, id string) string {
	if decision != nil {
		for _, target := range decision.Adopted {
			if target.ID == id {
				return target.Digest
			}
		}
	}
	return ""
}

func fragment(data []byte) ([]byte, string) {
	const start = "<!-- koinon:catalog-guidance -->"
	const end = "<!-- /koinon:catalog-guidance -->"
	first := bytes.Index(data, []byte(start))
	if first < 0 {
		return nil, "missing"
	}
	if bytes.Count(data, []byte(start)) != 1 || bytes.Count(data, []byte(end)) != 1 {
		return nil, "edited"
	}
	last := bytes.Index(data, []byte(end))
	if last < first {
		return nil, "edited"
	}
	last += len(end)
	if last < len(data) && data[last] == '\n' {
		last++
	}
	return data[first:last], ""
}

func inspectTarget(root ReadRoot, rootPath string, target Target, previous string, remaining *int) TargetState {
	result := TargetState{ID: target.ID}
	info, err := root.Lstat(target.Path)
	if errors.Is(err, fs.ErrNotExist) {
		result.State = "missing"
		return result
	}
	if err != nil {
		result.Reason = readReason(err)
		return result
	}
	var data []byte
	if target.Kind == "link" {
		if info.IsDir() {
			result.Reason = "directory_conflict"
			return result
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			result.Reason = "non_link_target"
			return result
		}
		link, err := root.Readlink(target.Path)
		if err != nil {
			result.Reason = readReason(err)
			return result
		}
		resolved := path.Clean(path.Join(path.Dir(target.Path), link))
		if path.IsAbs(link) {
			relative, err := filepath.Rel(rootPath, link)
			if err != nil || !safeCatalogPath(filepath.ToSlash(relative)) {
				result.Reason = "symlink_escape"
				return result
			}
			resolved = filepath.ToSlash(relative)
		}
		if !safeCatalogPath(resolved) {
			result.Reason = "symlink_escape"
			return result
		}
		// Root.Stat cannot follow a parent/link escape. A missing custom destination
		// remains edited link data, while the correct canonical link needs its target.
		destination, err := root.Stat(resolved)
		if err != nil && (!errors.Is(err, fs.ErrNotExist) || link == string(target.Expected)) {
			result.Reason = readReason(err)
			return result
		}
		if err == nil && !destination.IsDir() {
			result.Reason = "link_target_not_directory"
			return result
		}
		data = []byte(link)
	} else {
		if !info.Mode().IsRegular() {
			result.Reason = "non_regular_target"
			return result
		}
		data, err = root.ReadRegular(target.Path, *remaining)
		if err != nil {
			result.Reason = readReason(err)
			return result
		}
		*remaining -= len(data)
		if target.Kind == "fragment" {
			var state string
			data, state = fragment(data)
			if state != "" {
				result.State = state
				return result
			}
		}
	}
	result.Digest = digest(data)
	switch {
	case bytes.Equal(data, target.Expected):
		result.State = "present"
	case previous != "" && result.Digest == previous:
		result.State = "outdated"
	default:
		result.State = "edited"
	}
	return result
}

func aggregate(targets []TargetState, decision *Decision) (string, string) {
	missing, outdated, edited := false, false, false
	for _, target := range targets {
		if target.Reason != "" {
			return "", target.Reason
		}
		edited = edited || target.State == "edited"
		missing = missing || target.State == "missing"
		outdated = outdated || target.State == "outdated"
	}
	if edited {
		return "edited", ""
	}
	if missing {
		// A collection gaining a new target is outdated when every prior adopted
		// target still matches and only the new targets are absent.
		if decision != nil && len(targets) > 1 && len(decision.Adopted) > 0 {
			for _, old := range decision.Adopted {
				found := slices.IndexFunc(targets, func(t TargetState) bool { return t.ID == old.ID && t.Digest == old.Digest && old.Digest != "" })
				if found < 0 {
					return "missing", ""
				}
			}
			return "outdated", ""
		}
		return "missing", ""
	}
	if outdated {
		return "outdated", ""
	}
	return "present", ""
}

func inspectFacts(root ReadRoot, item Item) *FactState {
	if item.Facts == nil {
		return nil
	}
	state := &FactState{SchemaDigest: FactsDigest(item.Facts)}
	for _, field := range item.Facts.Fields {
		if field.Required {
			state.Required = append(state.Required, field.Heading)
		}
	}
	if root == nil {
		state.State, state.Reason = "unavailable", "project_context_unavailable"
		return state
	}
	info, err := root.Lstat(item.Facts.Filename)
	if errors.Is(err, fs.ErrNotExist) {
		state.State = "no-sidecar-needed"
		if len(state.Required) != 0 {
			state.State = "missing-required"
		}
		return state
	}
	if err != nil {
		state.State, state.Reason = "inspection-error", readReason(err)
		return state
	}
	if !info.Mode().IsRegular() {
		state.State, state.Reason = "inspection-error", "non_regular_target"
		return state
	}
	data, err := root.ReadRegular(item.Facts.Filename, MaxItemBytes)
	if err != nil {
		state.State, state.Reason = "inspection-error", readReason(err)
		return state
	}
	state.Digest = digest(data)
	if _, err := ParseFactValues(data, item.Name, item.Facts); err != nil {
		state.State = "invalid"
		if strings.HasPrefix(err.Error(), "missing fact:") {
			state.State = "incomplete"
		}
		return state
	}
	state.State = "present-valid"
	return state
}

// InspectItem returns hashes and states, never file bodies or native-home paths.
// Receipt selection/conflict and native directory verification precede this call.
func (i Inspector) InspectItem(c Catalog, context InspectionContext, id string, decision *Decision) ItemState {
	result := ItemState{ItemID: id, Scope: context.Scope, Targets: []TargetState{}}
	read, err := c.Read(context.Family, id)
	if err != nil {
		result.Reason = "item_unavailable"
		return result
	}
	item := read.Item
	project, projectErr := i.open(context.ProjectRoot)
	if projectErr == nil {
		defer project.Close()
	}
	result.Facts = inspectFacts(project, item)
	if result.Facts != nil && projectErr != nil && context.ProjectRoot != "" {
		result.Facts.Reason = readReason(projectErr)
	}
	targets, err := c.Targets(context.Family, context.Scope, id)
	if err != nil {
		result.Reason = "unsupported_scope"
		return result
	}
	rootPath, decisionRoot := context.ProjectRoot, context.Repository
	if context.Scope == "machine" {
		rootPath, decisionRoot = context.MachineRoot, context.MachineRoot
	}
	if decisionRoot != "" {
		result.ScopeDigest = ScopeDigest(context.Scope, decisionRoot)
	}
	if decision != nil {
		if decision.Validate() != nil || decision.Kind != "item" || decision.ItemID != id || decision.Family != context.Family ||
			decision.Scope != context.Scope || decision.ScopeDigest != result.ScopeDigest || CheckTargets(targets, decision.Adopted) != nil ||
			CheckTargets(targets, decision.Before) != nil || CheckTargets(targets, decision.Result) != nil {
			result.DecisionNote = "decision_unavailable"
		} else {
			result.Decision = decision
		}
	}
	root, err := i.open(rootPath)
	if err != nil {
		result.Reason = "scope_unavailable"
		return result
	}
	defer root.Close()
	remaining := MaxItemBytes
	for _, target := range targets {
		result.Targets = append(result.Targets, inspectTarget(root, rootPath, target, baseline(result.Decision, target.ID), &remaining))
	}
	result.State, result.Reason = aggregate(result.Targets, result.Decision)
	result.Collision, result.CollisionNote = i.inspectCollision(c, context, item)
	result.Recommended = len(item.FeatureRequirements) > 0 &&
		(result.State == "missing" || result.State == "outdated") && result.Reason == "" &&
		result.DecisionNote == "" && (result.Decision == nil || result.Decision.Choice != "declined") &&
		result.Collision != "name_conflict" && result.CollisionNote == ""
	return result
}

// inspectCollision reads only the two fixed same-name skill targets. It neither
// enumerates skill directories nor asserts which duplicate a native agent selects.
func (i Inspector) inspectCollision(c Catalog, context InspectionContext, item Item) (string, string) {
	if item.Kind != "skill" {
		return "", ""
	}
	observed := []TargetState{}
	for _, scope := range []string{"machine", "project"} {
		targets, err := c.Targets(context.Family, scope, item.ID)
		if err != nil {
			return "", "native_root_unverified"
		}
		rootPath := context.ProjectRoot
		if scope == "machine" {
			rootPath = context.MachineRoot
		}
		root, err := i.open(rootPath)
		if err != nil {
			return "", "collision_scope_unavailable"
		}
		remaining := MaxItemBytes
		state := inspectTarget(root, rootPath, targets[0], "", &remaining)
		root.Close()
		if state.Reason != "" {
			return "", state.Reason
		}
		observed = append(observed, state)
	}
	if observed[0].State == "missing" || observed[1].State == "missing" {
		return "", ""
	}
	if observed[0].Digest == observed[1].Digest {
		return "duplicate_discovery", ""
	}
	return "name_conflict", ""
}
