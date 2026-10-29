package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

// waitingPageSize bounds one page of the waiting items that the spoken alert reads.
const waitingPageSize = 200

// waitingPage is one page of the decisions and tasks that wait for the maintainer, with the
// decision change version, read in one transaction for the dashboard's spoken alert (#553).
// The browser reads every page; a version that changes between pages starts it again.
type waitingPage struct {
	Version string        `json:"version"`
	Items   []waitingItem `json:"items"`
	Next    int64         `json:"next,omitempty"`
	More    bool          `json:"more"`
}

// waitingItem is a waiting item's identity for the alert and the repository name it says.
type waitingItem struct {
	Reference  string `json:"reference"`
	Revision   int64  `json:"revision"`
	Kind       string `json:"kind"`
	Repository string `json:"repository"`
}

// waitingItems reads the page of waiting items with an ID above after, by ID.
func (s *Store) waitingItems(ctx context.Context, after int64) (waitingPage, error) {
	page := waitingPage{Items: []waitingItem{}}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT version FROM change_versions WHERE topic=?`, changeDecisions).Scan(&page.Version); err != nil {
		return page, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,reference,revision,kind,author_participant FROM decision_items
		WHERE state='active' AND kind!='fyi' AND id>? ORDER BY id LIMIT ?`, after, waitingPageSize+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var item waitingItem
		var participant string
		if err := rows.Scan(&id, &item.Reference, &item.Revision, &item.Kind, &participant); err != nil {
			return page, err
		}
		if len(page.Items) == waitingPageSize {
			page.More = true
			break
		}
		if parts := strings.Split(participant, "\x1f"); len(parts) == 3 {
			item.Repository = spokenRepository(parts[1])
		}
		page.Items = append(page.Items, item)
		page.Next = id
	}
	if !page.More {
		page.Next = 0
	}
	return page, rows.Err()
}

// spokenRepository is the name that the alert says for a repository: the base name of its
// directory, from the Git directory that a participant records.
func spokenRepository(path string) string {
	path = filepath.Clean(path)
	if filepath.Base(path) == ".git" {
		path = filepath.Dir(path)
	}
	name := strings.TrimSuffix(filepath.Base(path), ".git")
	if name == "." || name == "/" {
		return ""
	}
	return name
}

// dashboardWaiting serves GET /dashboard/waiting?after=ID as JSON to a signed-in browser.
func (d *Daemon) dashboardWaiting(w http.ResponseWriter, r *http.Request) {
	var after int64
	if value := r.URL.Query().Get("after"); value != "" {
		var err error
		if after, err = strconv.ParseInt(value, 10, 64); err != nil || after < 0 {
			http.Error(w, "after takes an item ID", http.StatusBadRequest)
			return
		}
	}
	page, err := d.store.waitingItems(r.Context(), after)
	if err != nil {
		http.Error(w, "waiting items not read", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(page)
}
