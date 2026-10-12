package library

import (
	"errors"
	"fmt"
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

func ParseFactValues(data []byte, name string, schema *Facts) (FactValues, error) {
	if schema == nil || len(data) == 0 || len(data) > MaxItemBytes || !utf8.Valid(data) {
		return FactValues{}, errors.New("invalid or oversized fact file")
	}
	text := string(data)
	if strings.ContainsFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) || strings.Contains(text, "$(") || strings.Contains(text, "${") {
		return FactValues{}, errors.New("fact data contains controls or shell expansion")
	}
	headings := map[string]string{}
	heading := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "## ") {
			heading = strings.TrimPrefix(line, "## ")
			if _, exists := headings[heading]; exists {
				return FactValues{}, errors.New("duplicate fact heading")
			}
			headings[heading] = ""
			continue
		}
		if heading == "" {
			if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "# ") {
				return FactValues{}, errors.New("unexpected fact preamble")
			}
			continue
		}
		headings[heading] += line + "\n"
	}
	for k, v := range headings {
		headings[k] = strings.TrimSpace(v)
	}
	if headings["Version"] != "1" || headings["Skill"] != name || headings["Sources"] == "" {
		return FactValues{}, errors.New("facts require Version 1, matching Skill and Sources")
	}
	values := FactValues{Fields: map[string]string{}, Sources: []string{}}
	for _, line := range strings.Split(headings["Sources"], "\n") {
		ref := strings.TrimPrefix(line, "- ")
		if ref == line || ref == "" || strings.ContainsAny(ref, "\\:#`") || path.IsAbs(ref) || path.Clean(ref) != ref || ref == "." || strings.HasPrefix(ref, "../") {
			return FactValues{}, errors.New("source must be a repository-relative evidence path")
		}
		values.Sources = append(values.Sources, ref)
	}
	delete(headings, "Version")
	delete(headings, "Skill")
	delete(headings, "Sources")
	for _, field := range schema.Fields {
		value, present := headings[field.Heading]
		if present && value == "" || !present && field.Required {
			return FactValues{}, fmt.Errorf("missing fact: %s", field.Heading)
		}
		if !present {
			if field.Default != nil {
				value = *field.Default
			}
		}
		values.Fields[field.Heading] = value
		delete(headings, field.Heading)
	}
	if len(headings) != 0 {
		return FactValues{}, errors.New("undeclared fact heading")
	}
	return values, nil
}
