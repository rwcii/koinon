package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rwcii/koinon/internal/capabilities"
)

func TestCatalogQueryPagesAndExactContent(t *testing.T) {
	cat, err := Embedded("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(cat)
	for _, family := range capabilities.Families() {
		t.Run(family, func(t *testing.T) {
			ids := []string{}
			after := ""
			for {
				page, err := cat.List(family, after, 3)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Items) > 3 || page.Digest != cat.Digest || page.RecommendedScope != "machine" || !slices.Equal(page.Features, capabilities.Features()) {
					t.Fatalf("page metadata: %+v", page)
				}
				for _, metadata := range page.Items {
					if metadata.Content != "" {
						t.Fatal("list returned content")
					}
					for _, d := range metadata.Destinations {
						if d.Family != family {
							t.Fatalf("foreign destination: %+v", d)
						}
					}
					read, err := cat.Read(family, metadata.ID)
					if err != nil {
						t.Fatal(err)
					}
					original := cat.Items[slices.IndexFunc(cat.Items, func(i Item) bool { return i.ID == metadata.ID })]
					if read.Item.Content != original.Content || read.Item.ContentDigest != digest([]byte(original.Content)) || read.Digest != cat.Digest || read.Item.RequiringRelease != "v1.2.3" {
						t.Fatalf("canonical read differs: %s", metadata.ID)
					}
					for _, d := range read.Item.Destinations {
						if d.Family != family {
							t.Fatalf("foreign read destination: %+v", d)
						}
					}
					ids = append(ids, metadata.ID)
				}
				if !page.More {
					if page.NextAfter != "" {
						t.Fatal("final cursor")
					}
					break
				}
				if page.NextAfter == after || len(page.Items) == 0 {
					t.Fatal("paging did not advance")
				}
				after = page.NextAfter
			}
			if len(ids) != len(cat.Items) || !slices.IsSorted(ids) {
				t.Fatal("incomplete catalog")
			}
			for i := 1; i < len(ids); i++ {
				if ids[i-1] == ids[i] {
					t.Fatal("duplicate item")
				}
			}
		})
	}
	after, _ := json.Marshal(cat)
	if string(before) != string(after) {
		t.Fatal("query changed embedded catalog")
	}
}

func TestQueryAcceptsEveryValidCanonicalName(t *testing.T) {
	source, permissions := sources(t)
	for _, name := range []string{"9-future", "-future", strings.Repeat("x", 64)} {
		source[name+"/SKILL.md"] = &fstest.MapFile{Data: synthetic(name)}
	}
	cat, err := Build(source, permissions, "dev")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"9-future", "-future", strings.Repeat("x", 64)} {
		if _, err := cat.Read("codex", "skill."+name); err != nil {
			t.Fatalf("valid canonical name %q unreadable: %v", name, err)
		}
		if _, err := cat.List("codex", "skill."+name, 1); err != nil {
			t.Fatalf("valid cursor %q unusable: %v", name, err)
		}
	}
}

func TestCatalogMetadataBytePaging(t *testing.T) {
	canonical, permissions := sources(t)
	source := fstest.MapFS{"AGENTS.md": canonical["AGENTS.md"]}
	fields := []FactField{}
	for i := 0; i < 31; i++ {
		value := strings.Repeat(`\`, 1000)
		fields = append(fields, FactField{Heading: fmt.Sprintf("Field %02d", i), Default: &value})
	}
	encodedFields, _ := json.Marshal(fields)
	for i := 0; i < MaxItems-5; i++ {
		name := fmt.Sprintf("byte-bound-%02d", i)
		content := strings.Replace(string(synthetic(name)), `"fields":[]`, `"fields":`+string(encodedFields), 1)
		source[name+"/SKILL.md"] = &fstest.MapFile{Data: []byte(content)}
	}
	cat, err := Build(source, permissions, "dev")
	if err != nil {
		t.Fatal(err)
	}
	after, count, pages := "", 0, 0
	for {
		page, err := cat.List("codex", after, MaxItems)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(page)
		if err != nil || len(encoded) > MaxPageBytes {
			t.Fatalf("oversized page %d: %v", len(encoded), err)
		}
		count += len(page.Items)
		pages++
		if !page.More {
			break
		}
		if page.NextAfter == after || len(page.Items) == 0 {
			t.Fatal("byte page cursor stalled")
		}
		after = page.NextAfter
	}
	if pages < 2 || count != len(cat.Items) {
		t.Fatalf("byte paging lost items: %d/%d in %d pages", count, len(cat.Items), pages)
	}
}

func TestCatalogQueryBounds(t *testing.T) {
	cat, err := Embedded("dev")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "../SKILL.md", "/tmp/SKILL.md", "https://example.invalid/skill", "skill.pickup\n", "skill."} {
		if _, err := cat.Read("codex", id); !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("invalid ID %q: %v", id, err)
		}
	}
	if _, err := cat.Read("codex", "skill.missing"); !errors.Is(err, ErrItemNotFound) {
		t.Fatal(err)
	}
	if _, err := cat.Read("foreign", "skill.pickup"); !errors.Is(err, ErrInvalidQuery) {
		t.Fatal(err)
	}
	for _, after := range []string{"../SKILL.md", "skill.missing"} {
		if _, err := cat.List("codex", after, 1); !errors.Is(err, ErrInvalidQuery) {
			t.Fatal(err)
		}
	}
	for _, n := range []int{-1, MaxItems + 1} {
		if _, err := cat.List("codex", "", n); !errors.Is(err, ErrInvalidQuery) {
			t.Fatal(err)
		}
	}
	page, err := cat.List("codex", "", 0)
	if err != nil || len(page.Items) != min(DefaultPageSize, len(cat.Items)) {
		t.Fatalf("default page: %+v %v", page, err)
	}
	if _, err := cat.List("foreign", "", 1); !errors.Is(err, ErrInvalidQuery) {
		t.Fatal(err)
	}
	last, err := cat.List("codex", cat.Items[len(cat.Items)-1].ID, 1)
	if err != nil || last.More || len(last.Items) != 0 {
		t.Fatalf("end page: %+v %v", last, err)
	}
}
