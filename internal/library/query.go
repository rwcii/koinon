package library

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"

	"github.com/rwcii/koinon/internal/capabilities"
)

var (
	ErrInvalidQuery = errors.New("invalid library query")
	ErrItemNotFound = errors.New("library item not found")
	itemID          = regexp.MustCompile(`^(skill|setup)\.[a-z0-9-]{1,64}$`)
)

const DefaultPageSize = 16

// MaxPageBytes keeps metadata pages within the existing transport response limits,
// including when future canonical descriptions and fact schemas grow.
const MaxPageBytes = 1 << 20

type Page struct {
	Version          int      `json:"version"`
	Digest           string   `json:"catalog_digest"`
	RecommendedScope string   `json:"recommended_scope"`
	Features         []string `json:"available_features"`
	Items            []Item   `json:"items"`
	More             bool     `json:"more"`
	NextAfter        string   `json:"next_after,omitempty"`
}

type ReadResult struct {
	Version int    `json:"version"`
	Digest  string `json:"catalog_digest"`
	Item    Item   `json:"item"`
}

func forFamily(item Item, family string) Item {
	item.Destinations = slices.Clone(item.Destinations)
	item.Destinations = slices.DeleteFunc(item.Destinations, func(d Destination) bool { return d.Family != family })
	return item
}

// List pages metadata without content. Family is supplied by a validated caller, never
// selected by an MCP argument. A daemon run retains one immutable embedded catalog.
func (c Catalog) List(family, after string, limit int) (Page, error) {
	if !slices.Contains(capabilities.Families(), family) || limit < 0 || limit > MaxItems {
		return Page{}, ErrInvalidQuery
	}
	if limit == 0 {
		limit = DefaultPageSize
	}
	start := 0
	if after != "" {
		if !itemID.MatchString(after) {
			return Page{}, ErrInvalidQuery
		}
		found := slices.IndexFunc(c.Items, func(i Item) bool { return i.ID == after })
		if found < 0 {
			return Page{}, ErrInvalidQuery
		}
		start = found + 1
	}
	end := min(start+limit, len(c.Items))
	p := Page{Version: c.Version, Digest: c.Digest, RecommendedScope: "machine", Features: capabilities.Features(), Items: []Item{}, More: end < len(c.Items)}
	used := 0
	for _, item := range c.Items[start:end] {
		item = forFamily(item, family)
		item.Content = ""
		encoded, err := json.Marshal(item)
		if err != nil {
			return Page{}, err
		}
		// Reserve room for the fixed page metadata and JSON array separators.
		if len(encoded) > MaxPageBytes-4096 {
			return Page{}, ErrInvalidQuery
		}
		if used+len(encoded)+1 > MaxPageBytes-4096 {
			break
		}
		p.Items = append(p.Items, item)
		used += len(encoded) + 1
	}
	end = start + len(p.Items)
	p.More = end < len(c.Items)
	if p.More {
		p.NextAfter = c.Items[end-1].ID
	}
	return p, nil
}

// Read returns the exact embedded bytes of one catalog item, with destination metadata
// for the caller's family. It reads no repository or native-home files.
func (c Catalog) Read(family, id string) (ReadResult, error) {
	if !slices.Contains(capabilities.Families(), family) || !itemID.MatchString(id) {
		return ReadResult{}, ErrInvalidQuery
	}
	for _, item := range c.Items {
		if item.ID == id {
			return ReadResult{Version: c.Version, Digest: c.Digest, Item: forFamily(item, family)}, nil
		}
	}
	return ReadResult{}, ErrItemNotFound
}
