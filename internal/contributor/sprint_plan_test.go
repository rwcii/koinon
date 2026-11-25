package contributor

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A sprint plan's chunk table gives each chunk's "Depends on" and "Owns", and its "Parallel
// view" lists groups of chunks that can run at the same time (#795, WDR 002, Decision 7).
// checkSprintPlan returns each fault of that view: a chunk with two rows or no owned item, a
// group number below 1 or with two rows, a chunk in no group or in two groups, a group with an
// unknown chunk, a dependency that is not in an earlier group, and two chunks of one group
// that own an item in common.

type sprintChunk struct {
	deps  []string
	owns  []ownedItem
	group int
}

// ownedItem is one comma-separated entry of an Owns cell: the paths in its backticks and the
// decision records that it names, or, when it has neither, its text. A record is held as the
// path of its file without the title, such as docs/engineering/wdr/002, so that the record's
// name, its file and its series directory overlap.
type ownedItem struct {
	paths []string
	name  string
}

// recordSeries maps a record kind to the directory of its series.
var recordSeries = map[string]string{"ADR": "docs/architecture/adr", "PDR": "docs/product/pdr", "WDR": "docs/engineering/wdr"}

var (
	chunkNumber  = regexp.MustCompile(`^\[?(\d{2})\b`)
	recordName   = regexp.MustCompile(`\b(ADR|PDR|WDR) ?(\d+)\b`)
	recordFile   = regexp.MustCompile(`^(docs/(?:architecture/adr|product/pdr|engineering/wdr)/\d{3})-[^/]*$`)
	backticked   = regexp.MustCompile("`([^`]+)`")
	parallelHead = regexp.MustCompile(`(?m)^#{2,3} Parallel view\s*$`)
)

func tableRows(lines []string) [][]string {
	var rows [][]string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			if len(rows) > 0 {
				break
			}
			continue
		}
		var cells []string
		for _, cell := range strings.Split(strings.Trim(line, "|"), "|") {
			cells = append(cells, strings.TrimSpace(cell))
		}
		if len(cells) > 0 && strings.Trim(cells[0], "-: ") == "" {
			continue
		}
		rows = append(rows, cells)
	}
	return rows
}

func column(header []string, name string) int {
	for i, cell := range header {
		if strings.EqualFold(cell, name) {
			return i
		}
	}
	return -1
}

// splitOwned splits an Owns cell at the commas outside backticks.
func splitOwned(cell string) []string {
	var items []string
	var b strings.Builder
	inside := false
	for _, r := range cell {
		switch {
		case r == '`':
			inside = !inside
			b.WriteRune(r)
		case r == ',' && !inside:
			items = append(items, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	items = append(items, b.String())
	var out []string
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" && !strings.EqualFold(item, "none") {
			out = append(out, item)
		}
	}
	return out
}

func parseOwned(text string) ownedItem {
	var item ownedItem
	m := backticked.FindStringSubmatch(text)
	whole := m != nil && m[0] == text
	for _, m := range backticked.FindAllStringSubmatch(text, -1) {
		// An entry that is one backticked token is a path, such as `internal` or `Makefile`;
		// in a longer entry, a backticked token is a path when it has a slash or a dot.
		if whole || strings.ContainsAny(m[1], "/.") {
			p := path.Clean(strings.TrimPrefix(m[1], "./"))
			if f := recordFile.FindStringSubmatch(p); f != nil {
				p = f[1]
			}
			item.paths = append(item.paths, p)
		}
	}
	for _, m := range recordName.FindAllStringSubmatch(backticked.ReplaceAllString(text, ""), -1) {
		n, _ := strconv.Atoi(m[2])
		item.paths = append(item.paths, fmt.Sprintf("%s/%03d", recordSeries[m[1]], n))
	}
	if len(item.paths) == 0 {
		item.name = strings.ToLower(strings.Join(strings.Fields(text), " "))
	}
	return item
}

// holds reports whether path a is path b or a directory that holds it.
func holds(a, b string) bool {
	return a == b || strings.HasPrefix(b, a+"/")
}

// overlap returns the shared part of two owned items, or "".
func overlap(a, b ownedItem) string {
	for _, p := range a.paths {
		for _, q := range b.paths {
			if holds(p, q) || holds(q, p) {
				return p + " and " + q
			}
		}
	}
	if a.name != "" && a.name == b.name {
		return a.name
	}
	return ""
}

func checkSprintPlan(text string) []string {
	var faults []string
	lines := strings.Split(text, "\n")
	view := parallelHead.FindStringIndex(text)
	if view == nil {
		return []string{"no Parallel view heading"}
	}
	viewLine := strings.Count(text[:view[0]], "\n")

	chunks := map[string]*sprintChunk{}
	var order []string
	rows := tableRows(lines[:viewLine])
	if len(rows) == 0 {
		return []string{"no chunk table before the Parallel view"}
	}
	header := rows[0]
	ci, di, oi := column(header, "Chunk"), column(header, "Depends on"), column(header, "Owns")
	if ci < 0 || di < 0 || oi < 0 {
		return []string{"the chunk table needs the columns Chunk, Depends on and Owns"}
	}
	for _, row := range rows[1:] {
		if len(row) <= ci || len(row) <= di || len(row) <= oi {
			faults = append(faults, "short chunk row: "+strings.Join(row, " | "))
			continue
		}
		m := chunkNumber.FindStringSubmatch(row[ci])
		if m == nil {
			faults = append(faults, "chunk cell without a number: "+row[ci])
			continue
		}
		if chunks[m[1]] != nil {
			faults = append(faults, "chunk "+m[1]+" has two rows")
			continue
		}
		c := &sprintChunk{}
		if dep := strings.TrimSpace(row[di]); !strings.EqualFold(dep, "none") {
			for _, d := range strings.Split(dep, ",") {
				c.deps = append(c.deps, strings.TrimSpace(d))
			}
		}
		for _, item := range splitOwned(row[oi]) {
			c.owns = append(c.owns, parseOwned(item))
		}
		if len(c.owns) == 0 {
			faults = append(faults, "chunk "+m[1]+" owns nothing")
		}
		chunks[m[1]] = c
		order = append(order, m[1])
	}

	if len(chunks) == 0 {
		return append(faults, "no chunk rows in the chunk table")
	}
	groups := tableRows(lines[viewLine+1:])
	if len(groups) == 0 {
		return append(faults, "no group table under the Parallel view")
	}
	gi, gc := column(groups[0], "Group"), column(groups[0], "Chunks")
	if gi < 0 || gc < 0 {
		return append(faults, "the Parallel view table needs the columns Group and Chunks")
	}
	members := map[int][]string{}
	var groupOrder []int
	for _, row := range groups[1:] {
		if len(row) <= gi || len(row) <= gc {
			faults = append(faults, "short group row: "+strings.Join(row, " | "))
			continue
		}
		g, err := strconv.Atoi(row[gi])
		if err != nil || g < 1 {
			faults = append(faults, "group without a number from 1: "+row[gi])
			continue
		}
		if _, seen := members[g]; seen {
			faults = append(faults, fmt.Sprintf("group %d has two rows", g))
			continue
		}
		members[g] = nil
		groupOrder = append(groupOrder, g)
		for _, n := range strings.Split(row[gc], ",") {
			n = strings.TrimSpace(n)
			c, ok := chunks[n]
			switch {
			case !ok:
				faults = append(faults, fmt.Sprintf("group %d names unknown chunk %s", g, n))
			case c.group != 0:
				faults = append(faults, fmt.Sprintf("chunk %s is in groups %d and %d", n, c.group, g))
			default:
				c.group = g
				members[g] = append(members[g], n)
			}
		}
	}
	if !sort.IntsAreSorted(groupOrder) {
		faults = append(faults, "groups are not in ascending order")
	}
	for _, n := range order {
		c := chunks[n]
		if c.group == 0 {
			faults = append(faults, "chunk "+n+" is in no group")
			continue
		}
		for _, d := range c.deps {
			dep, ok := chunks[d]
			switch {
			case !ok:
				faults = append(faults, "chunk "+n+" depends on unknown chunk "+d)
			case dep.group == 0:
			case dep.group >= c.group:
				faults = append(faults, fmt.Sprintf("chunk %s in group %d depends on chunk %s in group %d", n, c.group, d, dep.group))
			}
		}
	}
	for _, g := range groupOrder {
		list := members[g]
		for i := 0; i < len(list); i++ {
			for j := i + 1; j < len(list); j++ {
				for _, a := range chunks[list[i]].owns {
					for _, b := range chunks[list[j]].owns {
						if shared := overlap(a, b); shared != "" {
							faults = append(faults, fmt.Sprintf("group %d: chunks %s and %s both own %s", g, list[i], list[j], shared))
						}
					}
				}
			}
		}
	}
	return faults
}

func TestSprintPlansHaveValidParallelViews(t *testing.T) {
	plans, err := filepath.Glob(filepath.Join(root(t), "docs", "sprints", "*", "sprint.md"))
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, plan := range plans {
		b, err := os.ReadFile(plan)
		if err != nil {
			t.Fatal(err)
		}
		if !parallelHead.Match(b) {
			continue
		}
		checked++
		for _, fault := range checkSprintPlan(string(b)) {
			t.Errorf("%s: %s", plan, fault)
		}
	}
	if checked == 0 {
		t.Fatal("no sprint plan with a Parallel view; the Orca integration plan has one")
	}

}

const goodPlan = `# Plan

| Chunk | Complete outcome | Definition of done | Depends on | Owns |
| --- | --- | --- | --- | --- |
| [01 One](chunks/01-one.md) | a | a | none | ` + "`internal/a/`" + `, WDR 002 |
| [02 Two](chunks/02-two.md) | b | b | none | ` + "`internal/b/b.go`" + `, the ` + "`x`" + ` entry of ` + "`cmd/main.go`" + `, PDR 011 |
| [03 Three](chunks/03-three.md) | c | c | 01, 02 | ` + "`internal/a/c.go`" + `, WDR 002 |

## Parallel view

| Group | Chunks | Reason |
| --- | --- | --- |
| 1 | 01, 02 | no overlap |
| 2 | 03 | needs 01 and 02 |
`

func TestSprintPlanCheckControls(t *testing.T) {
	if faults := checkSprintPlan(goodPlan); len(faults) != 0 {
		t.Fatalf("good plan: %v", faults)
	}
	rep := func(plan, old, new string) string {
		if !strings.Contains(plan, old) {
			t.Fatalf("control text %q is not in the plan", old)
		}
		return strings.Replace(plan, old, new, 1)
	}
	replace := func(old, new string) string { return rep(goodPlan, old, new) }
	for _, c := range []struct {
		name, plan, want string
	}{
		{"chunk in two groups", replace("| 1 | 01, 02 |", "| 1 | 01, 02, 03 |"), "chunk 03 is in groups 1 and 2"},
		{"dependency in the same group", rep(replace("| 1 | 01, 02 |", "| 1 | 01, 02, 03 |"), "| 2 | 03 | needs 01 and 02 |\n", ""), "chunk 03 in group 1 depends on chunk 01 in group 1"},
		{"dependency in a later group", rep(replace("| 1 | 01, 02 |", "| 1 | 01 |"), "| 2 | 03 | needs 01 and 02 |", "| 2 | 03 | x |\n| 3 | 02 | y |"), "chunk 03 in group 2 depends on chunk 02 in group 3"},
		{"chunk in no group", replace("| 2 | 03 | needs 01 and 02 |\n", ""), "chunk 03 is in no group"},
		{"unknown chunk", replace("| 2 | 03 |", "| 2 | 03, 07 |"), "unknown chunk 07"},
		{"unknown dependency", replace("| 01, 02 |", "| 01, 09 |"), "depends on unknown chunk 09"},
		{"same file", replace("`internal/a/`,", "`internal/a/`, `cmd/main.go`,"), "chunks 01 and 02 both own cmd/main.go and cmd/main.go"},
		{"directory and a file in it", replace("`internal/b/b.go`", "`internal/a/x/y.go`"), "both own internal/a and internal/a/x/y.go"},
		{"same record", replace(", PDR 011 |", ", WDR 2 |"), "both own docs/engineering/wdr/002"},
		{"record and its file", replace(", PDR 011 |", ", `docs/engineering/wdr/002-authority.md` |"), "both own docs/engineering/wdr/002"},
		{"record and its series directory", replace(", PDR 011 |", ", `docs/engineering/wdr/` |"), "both own docs/engineering/wdr/002 and docs/engineering/wdr"},
		{"top-level directory", replace("`internal/a/`,", "`internal/`,"), "both own internal and internal/b/b.go"},
		{"bare top-level directory", replace("`internal/a/`,", "`internal`,"), "both own internal and internal/b/b.go"},
		{"bare file and its ./ form", rep(replace("`internal/a/`,", "`internal/a/`, `Makefile`,"), ", PDR 011 |", ", `./Makefile` |"), "both own Makefile and Makefile"},
		{"two rows for one chunk", replace("| [03 Three]", "| [02 Again](chunks/02-again.md) | b | b | none | `x/y.go` |\n| [03 Three]"), "chunk 02 has two rows"},
		{"empty owns", replace("| none | `internal/b/b.go`, the `x` entry of `cmd/main.go`, PDR 011 |", "| none |  |"), "chunk 02 owns nothing"},
		{"owns none", replace("| none | `internal/b/b.go`, the `x` entry of `cmd/main.go`, PDR 011 |", "| none | none |"), "chunk 02 owns nothing"},
		{"group below 1", replace("| 2 | 03 |", "| -1 | 03 |"), "group without a number from 1: -1"},
		{"two rows for one group", replace("| 2 | 03 |", "| 1 | 03 |"), "group 1 has two rows"},
		{"same named item", rep(replace("`internal/a/`, WDR 002 |", "`internal/a/`, USAGE \"Run\" |"), ", PDR 011 |", ", usage  \"Run\" |"), "both own usage \"run\""},
		{"no parallel view", replace("## Parallel view", "## Order"), "no Parallel view heading"},
	} {
		t.Run(c.name, func(t *testing.T) {
			faults := checkSprintPlan(c.plan)
			if len(faults) == 0 {
				t.Fatalf("no fault for %s", c.name)
			}
			if c.want != "" && !strings.Contains(strings.Join(faults, "\n"), c.want) {
				t.Fatalf("faults %v do not contain %q", faults, c.want)
			}
		})
	}
}
