package core

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
)

// Dashboard sorting (#222). Each view allowlists its sortable columns in a sortSpec. The
// request names only a column key and a direction; the SQL expressions come from the spec,
// never from the request. Paging is by keyset: a cursor holds the last listed row's sort
// values and tie-breakers, so a page neither repeats nor skips a row when rows are added.

// sortTerm is one value of a row's sort key.
type sortTerm struct {
	expr string // SQL expression over the view's named columns; empty for in-memory views
	text bool   // a text value; otherwise an integer
}

type sortColumn struct {
	key   string
	terms []sortTerm
	desc  bool // the direction of the first click
}

type sortSpec struct {
	columns []sortColumn
	tie     []sortTerm // follows every column's terms, so the order is total
}

// The first column is the default.
func (s *sortSpec) column(key string) sortColumn {
	for _, c := range s.columns {
		if c.key == key {
			return c
		}
	}
	return s.columns[0]
}

// dashboardSort is one request's order and position, and the filters its links keep.
type dashboardSort struct {
	Key    string
	Desc   bool
	spec   *sortSpec
	path   string
	params url.Values
	terms  []sortTerm
	after  []any // the cursor's values; nil for the first page
}

// parseSort reads sort, dir and after from a view's query. An unknown column gives the
// default order; a malformed cursor is invalid. keep names the filter parameters that
// sort and page links carry.
func parseSort(spec *sortSpec, path string, q url.Values, keep ...string) (dashboardSort, error) {
	c := spec.column(q.Get("sort"))
	result := dashboardSort{Key: c.key, Desc: c.desc, spec: spec, path: path, params: url.Values{},
		terms: append(append([]sortTerm{}, c.terms...), spec.tie...)}
	if c.key == q.Get("sort") {
		switch q.Get("dir") {
		case "asc":
			result.Desc = false
		case "desc":
			result.Desc = true
		}
	}
	for _, name := range keep {
		if v := q.Get(name); v != "" {
			result.params.Set(name, v)
		}
	}
	if v := q.Get("after"); v != "" {
		var err error
		if result.after, err = decodeCursor(v, result.terms); err != nil {
			return result, err
		}
	}
	return result, nil
}

func decodeCursor(v string, terms []sortTerm) ([]any, error) {
	data, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw []any
	if decoder.Decode(&raw) != nil || decoder.More() || len(raw) != len(terms) {
		return nil, ErrInvalid
	}
	values := make([]any, len(raw))
	for i, term := range terms {
		switch x := raw[i].(type) {
		case string:
			if !term.text {
				return nil, ErrInvalid
			}
			values[i] = x
		case json.Number:
			n, err := x.Int64()
			if term.text || err != nil {
				return nil, ErrInvalid
			}
			values[i] = n
		default:
			return nil, ErrInvalid
		}
	}
	return values, nil
}

// cursor encodes a listed row's sort values for the next page's link.
func (s dashboardSort) cursor(values []any) string {
	data, _ := json.Marshal(values)
	return base64.RawURLEncoding.EncodeToString(data)
}

// Dir is the direction as a link names it.
func (s dashboardSort) Dir() string {
	if s.Desc {
		return "desc"
	}
	return "asc"
}

// sql returns the keyset condition (or "1") with its arguments, and the ORDER BY list.
func (s dashboardSort) sql() (where string, args []any, order string) {
	exprs := make([]string, len(s.terms))
	ordered := make([]string, len(s.terms))
	for i, t := range s.terms {
		exprs[i], ordered[i] = t.expr, t.expr+" "+strings.ToUpper(s.Dir())
	}
	where = "1"
	if s.after != nil {
		op := ">"
		if s.Desc {
			op = "<"
		}
		where = "(" + strings.Join(exprs, ",") + ")" + op + "(" + strings.TrimSuffix(strings.Repeat("?,", len(exprs)), ",") + ")"
		args = s.after
	}
	return where, args, strings.Join(ordered, ",")
}

// selectList is the sort expressions, to scan each row's cursor values.
func (s dashboardSort) selectList() string {
	exprs := make([]string, len(s.terms))
	for i, t := range s.terms {
		exprs[i] = t.expr
	}
	return strings.Join(exprs, ",")
}

// scanTargets returns fresh destinations for one row's sort values.
func (s dashboardSort) scanTargets() []any {
	targets := make([]any, len(s.terms))
	for i, t := range s.terms {
		if t.text {
			targets[i] = new(string)
		} else {
			targets[i] = new(int64)
		}
	}
	return targets
}

func scannedValues(targets []any) []any {
	values := make([]any, len(targets))
	for i, t := range targets {
		switch x := t.(type) {
		case *string:
			values[i] = *x
		case *int64:
			values[i] = *x
		}
	}
	return values
}

// compareValues orders two sort keys of the same terms.
func compareValues(a, b []any) int {
	for i := range a {
		var c int
		switch x := a[i].(type) {
		case string:
			c = cmp.Compare(x, b[i].(string))
		case int64:
			c = cmp.Compare(x, b[i].(int64))
		case float64:
			c = cmp.Compare(x, b[i].(float64))
		}
		if c != 0 {
			return c
		}
	}
	return 0
}

// before reports whether sort key a comes before b in this order.
func (s dashboardSort) before(a, b []any) bool {
	c := compareValues(a, b)
	if s.Desc {
		return c > 0
	}
	return c < 0
}

func (s dashboardSort) link(key, dir, after string) string {
	v := url.Values{}
	for name, values := range s.params {
		v[name] = values
	}
	v.Set("sort", key)
	v.Set("dir", dir)
	if after != "" {
		v.Set("after", after)
	}
	return s.path + "?" + v.Encode()
}

// Next is the link to the page after a cursor, in the same order and filters.
func (s dashboardSort) Next(cursor string) string {
	return s.link(s.Key, s.Dir(), cursor)
}

type sortHeader struct {
	Label, Href, Sort string // Sort is the aria-sort value of the current column
}

// Column is a sortable header: a link to the first page in that column's order, in its
// first direction. The current column's link reverses the direction.
func (s dashboardSort) Column(key, label string) sortHeader {
	h, dir, c := sortHeader{Label: label}, "asc", s.spec.column(key)
	switch {
	case key == s.Key:
		h.Sort = s.Dir() + "ending"
		if !s.Desc {
			dir = "desc"
		}
	case c.key == key && c.desc:
		dir = "desc"
	}
	h.Href = s.link(key, dir, "")
	return h
}
