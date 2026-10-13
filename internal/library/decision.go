package library

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rwcii/koinon/internal/capabilities"
)

const (
	DecisionMarker   = "koinon-library-decision"
	DecisionVersion  = 1
	MaxDecisionBytes = 8192 // machine record; project admission uses memory's body limit
	MaxTargets       = 32
)

var (
	ErrInvalidDecision = errors.New("invalid library decision")
	digestPattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	targetPattern      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}(?:\.[a-z][a-z0-9-]{0,63})?$`)
)

// TargetDigest identifies a catalog-derived target. It never supplies a path to read.
// An empty digest records a verified absent target.
type TargetDigest struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// Decision is an outcome reported by an agent, not an attestation of content or approval.
// Project decisions use memory_record; machine decisions use compared revision state.
// Adopted retains the last adopted baseline independently of an update decline.
type Decision struct {
	Marker        string         `json:"library"`
	Version       int            `json:"version"`
	ItemID        string         `json:"item_id"`
	Family        string         `json:"family"`
	Scope         string         `json:"scope"`
	ScopeDigest   string         `json:"scope_digest"`
	CatalogDigest string         `json:"catalog_digest"`
	ItemDigest    string         `json:"item_digest"`
	Kind          string         `json:"kind"` // item or repository-facts
	Choice        string         `json:"decision"`
	Evidence      string         `json:"evidence"`
	Before        []TargetDigest `json:"pre_edit"`
	Result        []TargetDigest `json:"result"`
	Adopted       []TargetDigest `json:"adopted"`
	SchemaDigest  string         `json:"schema_digest,omitempty"`
}

func ValidDigest(value string) bool { return digestPattern.MatchString(value) }

func validTargets(targets []TargetDigest) bool {
	if len(targets) > MaxTargets {
		return false
	}
	seen := map[string]bool{}
	for _, target := range targets {
		if !targetPattern.MatchString(target.ID) || seen[target.ID] || target.Digest != "" && !ValidDigest(target.Digest) {
			return false
		}
		seen[target.ID] = true
	}
	return true
}

// Validate checks the bounded receipt schema. Identity and current catalog/scope checks
// belong to its native admission path; a claimed family is not caller identity.
func (d Decision) Validate() error {
	if d.Marker != DecisionMarker || d.Version != DecisionVersion || !itemID.MatchString(d.ItemID) ||
		!slices.Contains(capabilities.Families(), d.Family) || d.Scope != "machine" && d.Scope != "project" ||
		!ValidDigest(d.ScopeDigest) || !ValidDigest(d.CatalogDigest) || !ValidDigest(d.ItemDigest) ||
		d.Choice != "adopted" && d.Choice != "declined" || d.Kind != "item" && d.Kind != "repository-facts" ||
		d.Kind == "repository-facts" && (d.Scope != "project" || !ValidDigest(d.SchemaDigest)) ||
		d.Kind == "item" && d.SchemaDigest != "" || !utf8.ValidString(d.Evidence) || strings.TrimSpace(d.Evidence) == "" ||
		len(d.Evidence) > 512 || strings.ContainsFunc(d.Evidence, unicode.IsControl) ||
		!validTargets(d.Before) || !validTargets(d.Result) || !validTargets(d.Adopted) {
		return ErrInvalidDecision
	}
	if d.Choice == "adopted" && len(d.Result) == 0 {
		return ErrInvalidDecision
	}
	return nil
}

// ParseDecision accepts inert strict JSON; it resolves no path and imports no fact body.
// It also parses retained receipts whose catalog item has since been removed.
func ParseDecision(data []byte) (Decision, error) {
	if len(data) == 0 || len(data) > MaxItemBytes || !utf8.Valid(data) {
		return Decision{}, ErrInvalidDecision
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var d Decision
	if dec.Decode(&d) != nil || dec.Decode(new(any)) != io.EOF || d.Validate() != nil ||
		d.Scope == "machine" && len(data) > MaxDecisionBytes {
		return Decision{}, ErrInvalidDecision
	}
	return d, nil
}

func OrderedTargets(targets []TargetDigest) []TargetDigest {
	result := slices.Clone(targets)
	if result == nil {
		result = []TargetDigest{}
	}
	slices.SortFunc(result, func(a, b TargetDigest) int { return strings.Compare(a.ID, b.ID) })
	return result
}

// Encode canonicalizes target order for exact no-op comparison. It does not change the
// adopted baseline: the decision recorder preserves that through a decline.
func (d Decision) Encode() ([]byte, error) {
	if d.Validate() != nil {
		return nil, ErrInvalidDecision
	}
	d.Before, d.Result, d.Adopted = OrderedTargets(d.Before), OrderedTargets(d.Result), OrderedTargets(d.Adopted)
	data, err := json.Marshal(d)
	if err != nil || len(data) > MaxItemBytes || d.Scope == "machine" && len(data) > MaxDecisionBytes {
		return nil, ErrInvalidDecision
	}
	return data, nil
}

// FactsDigest names the portable schema, never the adopting project's fact values.
func FactsDigest(facts *Facts) string {
	if facts == nil {
		return ""
	}
	data, _ := json.Marshal(facts)
	return digest(data)
}
