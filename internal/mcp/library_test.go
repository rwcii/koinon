package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/library"
)

func TestLibraryToolsEveryNativeFamily(t *testing.T) {
	cat, err := library.Embedded("dev")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range callers {
		t.Run(c.family, func(t *testing.T) {
			h := c.harness(t)
			canary := filepath.Join(h.s.c.Directory, "adopter-local.txt")
			data := []byte("synthetic local content retained")
			if err := os.WriteFile(canary, data, 0600); err != nil {
				t.Fatal(err)
			}
			call := func(name string, args map[string]any) map[string]any {
				for k, v := range c.args {
					args[k] = v
				}
				r, failed := h.tool(name, args, c.meta)
				if failed {
					t.Fatalf("%s: %v", name, r)
				}
				return r["library"].(map[string]any)
			}
			r := call("library_list", map[string]any{"limit": 2})
			if r["more"] != true || r["catalog_digest"] != cat.Digest || len(r["items"].([]any)) != 2 {
				t.Fatal(r)
			}
			for _, item := range r["items"].([]any) {
				if _, found := item.(map[string]any)["content"]; found {
					t.Fatal("metadata list includes content")
				}
			}
			read := call("library_read", map[string]any{"item_id": "skill.pickup"})
			item := read["item"].(map[string]any)
			want, err := cat.Read(c.family, "skill.pickup")
			if err != nil || item["content"] != want.Item.Content || item["content_digest"] != want.Item.ContentDigest {
				t.Fatal("canonical content mismatch", err)
			}
			for _, d := range item["destinations"].([]any) {
				if d.(map[string]any)["family"] != c.family {
					t.Fatal("foreign family metadata")
				}
			}
			for _, bad := range []map[string]any{{"caller": "other"}, {"family": "other"}, {"item_id": "skill.pickup", "path": canary}, {"item_id": "skill.pickup", "url": "https://example.invalid"}} {
				for k, v := range c.args {
					bad[k] = v
				}
				if _, failed := h.tool("library_read", bad, c.meta); !failed {
					t.Fatal("caller/path/URL argument accepted")
				}
			}
			if body, err := os.ReadFile(canary); err != nil || !bytes.Equal(body, data) {
				t.Fatal("read changed adopter file", err)
			}
			encoded, _ := json.Marshal(read)
			if bytes.Contains(encoded, []byte(h.root)) || bytes.Contains(encoded, []byte(h.s.c.Directory)) {
				t.Fatal("private runtime path in catalog")
			}
			if len(h.sessions()) != 1 || h.sessions()[0].Address != "" {
				t.Fatal("catalog read changed plain-peer admission")
			}
		})
	}
}

func TestLibraryOfferEveryInitializeWithoutRegistration(t *testing.T) {
	h := newHarness(t, "")
	for i := 0; i < 3; i++ {
		r := h.request("initialize", map[string]any{"clientInfo": map[string]any{"name": "synthetic-client"}})
		instructions := r["result"].(map[string]any)["instructions"].(string)
		for _, text := range []string{"library_list", "library_read", "machine-wide skills are the default", "library_state", "library_decision", "memory_record"} {
			if !strings.Contains(instructions, text) {
				t.Fatalf("missing offer %q", text)
			}
		}
		if len(h.sessions()) != 0 {
			t.Fatal("initialize registered a session")
		}
	}
	if reply, failed := h.tool("library_list", map[string]any{}, nil); !failed || reply["code"] != "identity_unavailable" {
		t.Fatal("query guessed identity", reply)
	}
}
