package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A directory refusal with a long Handoff path keeps its check within MaxRefusalMessage, so
// the native client passes it on (#468).
func TestArtifactRefusalReachesClient(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	facts := filepath.Join(f.repo, ".koinon", "skills", "handoff.md")
	if err := os.MkdirAll(filepath.Dir(facts), 0700); err != nil {
		t.Fatal(err)
	}
	// A clean relative path well below PATH_MAX and the 64 KiB fact bound.
	dir := strings.Repeat("nested/", 170) + "<agent>/"
	text := "# Repository facts — handoff\n\n## Version\n1\n## Skill\nhandoff\n## Sources\n- AGENTS.md\n## Handoff path\n" + dir + "\n"
	if err := os.WriteFile(facts, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	_, artifactErr := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, v.Revision, "2026-10-13-120000.md")
	if !artifactRefused(artifactErr, "must be in ") {
		t.Fatalf("expected directory refusal, got %v", artifactErr)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failure(w, artifactErr)
	}))
	defer server.Close()
	_, err := Call(context.Background(), strings.TrimPrefix(server.URL, "http://"), "synthetic", "/v1/cycles/ready", map[string]any{})
	var refused RefusedError
	if !errors.As(err, &refused) || refused.Code != ErrCycleArtifact.Code || !strings.Contains(refused.Message, "must be in ") {
		t.Fatalf("directory check was lost at the native client: code=%q message=%q, error=%v", refused.Code, refused.Message, err)
	}
}
