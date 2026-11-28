package library

import (
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// FactValues validates inert sidecar text against the canonical declaration. It does not
// read paths, expand commands or execute content. File/target inspection belongs to adoption.
type FactValues struct {
	Sources []string
	Fields  map[string]string
}

// retiredFacts are the headings that a skill no longer declares but that a facts file written
// for an earlier catalog can still hold. Such a heading stays valid, and its value is ignored.
// ship's Decision record chose a record mode for a record file, which Koinon no longer writes
// (#623).
var retiredFacts = map[string][]string{"ship": {"Decision record"}}

// FactError is a refused facts file. Code names the cause (#818); Heading is the schema
// heading of a missing fact, never text from the file.
type FactError struct {
	Code    string
	Heading string
}

func (e FactError) Error() string {
	if e.Heading != "" {
		return e.Code + ": " + e.Heading
	}
	return e.Code
}

// The FactError codes, one for each refusal cause.
const (
	FactsTextInvalid       = "facts_text_invalid"       // empty, over the size bound, not UTF-8, or with control characters
	FactsPreamble          = "facts_preamble"           // a line before the first "## " heading that is not blank or a "# " title
	FactsHeadingDuplicate  = "facts_heading_duplicate"  // a heading given twice
	FactsVersion           = "facts_version"            // Version is not 1
	FactsSkillMismatch     = "facts_skill_mismatch"     // Skill is not the item's name
	FactsSourcesMissing    = "facts_sources_missing"    // Sources is empty or absent
	FactsSourceInvalid     = "facts_source_invalid"     // a Sources line without "- ", or a path that is not repository-relative
	FactsFieldMissing      = "facts_field_missing"      // a required heading is absent, or a heading is present and empty
	FactsHeadingUndeclared = "facts_heading_undeclared" // a heading that the item's schema does not declare
)

func ParseFactValues(data []byte, name string, schema *Facts) (FactValues, error) {
	if schema == nil || len(data) == 0 || len(data) > MaxItemBytes || !utf8.Valid(data) {
		return FactValues{}, FactError{Code: FactsTextInvalid}
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.ContainsFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
		return FactValues{}, FactError{Code: FactsTextInvalid}
	}
	headings := map[string]string{}
	heading := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "## ") {
			heading = strings.TrimPrefix(line, "## ")
			if _, exists := headings[heading]; exists {
				return FactValues{}, FactError{Code: FactsHeadingDuplicate}
			}
			headings[heading] = ""
			continue
		}
		if heading == "" {
			if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "# ") {
				return FactValues{}, FactError{Code: FactsPreamble}
			}
			continue
		}
		headings[heading] += line + "\n"
	}
	for k, v := range headings {
		headings[k] = strings.TrimSpace(v)
	}
	switch {
	case headings["Version"] != "1":
		return FactValues{}, FactError{Code: FactsVersion}
	case headings["Skill"] != name:
		return FactValues{}, FactError{Code: FactsSkillMismatch}
	case headings["Sources"] == "":
		return FactValues{}, FactError{Code: FactsSourcesMissing}
	}
	values := FactValues{Fields: map[string]string{}, Sources: []string{}}
	for _, line := range strings.Split(headings["Sources"], "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		ref := strings.TrimPrefix(line, "- ")
		if ref == line || ref == "" || strings.ContainsAny(ref, "\\:#`") || path.IsAbs(ref) || path.Clean(ref) != ref || ref == "." || strings.HasPrefix(ref, "../") {
			return FactValues{}, FactError{Code: FactsSourceInvalid}
		}
		values.Sources = append(values.Sources, ref)
	}
	delete(headings, "Version")
	delete(headings, "Skill")
	delete(headings, "Sources")
	for _, field := range schema.Fields {
		value, present := headings[field.Heading]
		if present && value == "" || !present && field.Required {
			return FactValues{}, FactError{Code: FactsFieldMissing, Heading: field.Heading}
		}
		if !present {
			if field.Default != nil {
				value = *field.Default
			}
		}
		values.Fields[field.Heading] = value
		delete(headings, field.Heading)
	}
	for _, heading := range retiredFacts[name] {
		delete(headings, heading)
	}
	if len(headings) != 0 {
		return FactValues{}, FactError{Code: FactsHeadingUndeclared}
	}
	return values, nil
}
