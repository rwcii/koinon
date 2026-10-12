package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rwcii/koinon/internal/library"
)

func libraryRows(t *testing.T, s *Store) []byte {
	t.Helper()
	all := map[string][][]any{}
	for _, table := range []string{"sessions", "memory_stores", "memory_entries", "memory_idem", "memory_cursors", "memory_snapshots", "memory_snapshot_items", "work_items", "work_scope_revisions", "work_events", "work_replays"} {
		rows, err := s.db.Query("SELECT * FROM " + table + " ORDER BY 1,2")
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(cols))
			refs := make([]any, len(cols))
			for i := range values {
				refs[i] = &values[i]
			}
			if err := rows.Scan(refs...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			all[table] = append(all[table], values)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
	}
	data, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestLibraryRoutesReadOnlyForEveryFamily(t *testing.T) {
	d, root := startTestDaemon(t)
	repo := testRepo(t)
	home := t.TempDir()
	marker := []byte("synthetic adopter data")
	projectFile, nativeFile := filepath.Join(repo, "local-skill.md"), filepath.Join(home, "local-skill.md")
	for _, name := range []string{projectFile, nativeFile} {
		if err := os.WriteFile(name, marker, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CODEX_HOME", home)
	for _, family := range SupportedFamilies() {
		r := Registration{Family: family, ID: "synthetic-library", Directory: repo, Repository: repo}
		if family == "claude" {
			r.WakeTarget = json.RawMessage(`{"claude_pid":4242}`)
		}
		if _, err := d.store.Register(context.Background(), r); err != nil {
			t.Fatalf("%s registration: %v", family, err)
		}
	}
	// Existing memory and a frozen snapshot must stay unchanged, including acknowledgement.
	m, err := d.store.ResolveMemoryCaller(context.Background(), Key{"codex", "synthetic-library"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.MemoryRecord(context.Background(), m, MemoryRecordRequest{Type: "finding", Body: "synthetic retained memory"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.MemorySync(context.Background(), m, MemorySyncRequest{}); err != nil {
		t.Fatal(err)
	}
	before := libraryRows(t, d.store)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range SupportedFamilies() {
		for _, op := range []string{"list", "read"} {
			body := map[string]any{"caller": Key{family, "synthetic-library"}}
			if op == "read" {
				body["item_id"] = "skill.pickup"
			}
			data, err := Call(context.Background(), d.Addresses()[0], secret, "/v1/library/"+op, body)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte(repo)) || bytes.Contains(data, []byte(home)) || bytes.Contains(data, []byte("synthetic-library")) {
				t.Fatal("private caller context returned")
			}
			if op == "read" {
				var reply struct {
					Library library.ReadResult `json:"library"`
				}
				if err := json.Unmarshal(data, &reply); err != nil {
					t.Fatal(err)
				}
				want, _ := d.catalog.Read(family, "skill.pickup")
				if reply.Library.Item.Content != want.Item.Content || reply.Library.Digest != d.catalog.Digest {
					t.Fatal("embedded content mismatch")
				}
				for _, target := range reply.Library.Item.Destinations {
					if target.Family != family {
						t.Fatal("unfiltered family")
					}
				}
			}
		}
	}
	if after := libraryRows(t, d.store); !bytes.Equal(before, after) {
		t.Fatal("catalog read wrote session, memory, acknowledgement or work rows")
	}
	for _, name := range []string{projectFile, nativeFile} {
		b, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(b, marker) {
			t.Fatal("catalog read changed adopter files", err)
		}
	}
}

func TestLibraryRoutesIdentityAndArguments(t *testing.T) {
	d, root := startTestDaemon(t)
	if _, err := d.store.Register(context.Background(), Registration{Family: "deepseek", ID: "synthetic-library", Directory: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	secret, _ := ReadSecret(root)
	valid := `{"caller":{"family":"deepseek","id":"synthetic-library"}}`
	if r := request(t, d, "/v1/library/list", valid, ""); r.StatusCode != http.StatusUnauthorized {
		t.Fatal("unauthenticated catalog")
	}
	for _, c := range []struct {
		op, body, code string
		status         int
	}{
		{"list", `{}`, "invalid_request", 400},
		{"list", `{"caller":{"family":"foreign","id":"synthetic"}}`, "invalid_request", 400},
		{"list", `{"caller":{"family":"deepseek","id":"missing"}}`, "caller_inactive", 403},
		{"list", `{"caller":{"family":"deepseek","id":"synthetic-library"},"limit":33}`, "invalid_request", 400},
		{"list", `{"caller":{"family":"deepseek","id":"synthetic-library"},"after":"https://example.invalid"}`, "invalid_request", 400},
		{"read", `{"caller":{"family":"deepseek","id":"synthetic-library"},"item_id":"../local.md"}`, "invalid_request", 400},
		{"read", `{"caller":{"family":"deepseek","id":"synthetic-library"},"item_id":"skill.missing"}`, "library_item_not_found", 404},
		{"read", `{"caller":{"family":"deepseek","id":"synthetic-library"},"item_id":"skill.pickup","path":"/synthetic"}`, "invalid_request", 400},
	} {
		r := request(t, d, "/v1/library/"+c.op, c.body, secret)
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var value struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(b, &value); err != nil || r.StatusCode != c.status || value.Code != c.code {
			t.Fatalf("%s: %d %s %v", c.op, r.StatusCode, b, err)
		}
	}
}
