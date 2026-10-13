package core

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
)

// decisionsData is the read-only Decisions view (#206, chunk 01): one page of active items
// and an optional item found by reference. Chunk 03 replaces it with the designed board.
type decisionsData struct {
	Page      DecisionPage
	Lookup    string
	Item      *DecisionItem
	LookupErr string
}

func (s *Store) dashboardDecisions(ctx context.Context, after, reference string) (decisionsData, error) {
	var data decisionsData
	from, err := strconv.ParseInt(after, 10, 64)
	if after != "" && (err != nil || from < 0) {
		from = 0
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return data, err
	}
	defer tx.Rollback()
	if data.Page, err = decisionPage(ctx, tx, "active", nil, from, decisionPageItems); err != nil {
		return data, err
	}
	if reference != "" {
		data.Lookup = reference
		if checkReference(reference) != nil {
			data.LookupErr = "not a valid reference"
			return data, nil
		}
		item, err := decisionItem(ctx, tx, reference)
		if code := errorCode(err); code == "not_found" {
			data.LookupErr = "no decision item has this reference"
		} else if err != nil {
			return data, err
		} else {
			data.Item = &item
		}
	}
	return data, nil
}

// decisionFormLimit fits a 4 KiB note, percent-encoded, with the other fields.
const decisionFormLimit = 3*decisionNoteLimit + 4096

// decisionActions adds the maintainer's verdicts (#206, chunk 02): Approve and Decline on a
// decision item and Dismiss on an fyi item, on the revision the page showed. They are
// dashboard actions only, behind the login, Host, Origin and CSRF checks.
func (d *Daemon) decisionActions(mux *http.ServeMux, authed func(int64, func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc,
	run func(w http.ResponseWriter, r *http.Request, action, target, view string, query url.Values, valid bool,
		apply func(ctx context.Context) error, accepted string, refusal func(error) string)) {
	for action, verdict := range map[string]string{"decision-approve": "approved", "decision-decline": "declined", "decision-dismiss": "dismissed"} {
		action, verdict := action, verdict
		mux.HandleFunc("POST /dashboard/actions/"+action, authed(decisionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
			reference, note := r.PostForm.Get("reference"), r.PostForm.Get("note")
			revision, ok := formInt(r, "revision")
			valid := ok && revision > 0 && checkReference(reference) == nil
			query := url.Values{}
			if valid {
				query.Set("ref", reference)
			}
			run(w, r, action, "decision "+reference, "decisions", query, valid, func(ctx context.Context) error {
				_, err := d.store.DecisionVerdict(ctx, reference, revision, verdict, note)
				return err
			}, "decision_"+verdict, nil)
		}))
	}
}
