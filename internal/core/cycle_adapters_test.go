package core

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// A test that changes cycleAdapters does so before startTestDaemon: the daemon's cycle loop
// reads the map, and t.Cleanup runs last in, first out, so a change and its restore registered
// after the start would run while the daemon still runs (#608).
func TestCycleAdaptersSwappedBeforeTestDaemon(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("test files %v %v", files, err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found, late := lateCycleAdapterWrites(fset, file)
		checked += found
		for _, l := range late {
			t.Error(l)
		}
	}
	if checked < 4 {
		t.Fatalf("checked %d tests that start a daemon and change cycleAdapters, want at least 4", checked)
	}
}

// The scan finds an assignment, an indexed write and a delete after the start, in the test's
// body or in a closure such as its cleanup, and accepts each before the start.
func TestLateCycleAdapterWrites(t *testing.T) {
	for _, c := range []struct {
		name, body string
		late       int
	}{
		{"swap before", "previous := cycleAdapters\ncycleAdapters = nil\nt.Cleanup(func() { cycleAdapters = previous })\nstartTestDaemon(t)", 0},
		{"index before", "cycleAdapters[\"claude\"] = nil\nstartTestDaemon(t)", 0},
		{"swap after", "startTestDaemon(t)\ncycleAdapters = nil", 1},
		{"restore after", "startTestDaemon(t)\nt.Cleanup(func() { cycleAdapters = nil })", 1},
		{"index after", "startTestDaemon(t)\ncycleAdapters[\"claude\"] = nil", 1},
		{"delete after", "startTestDaemon(t)\ndelete(cycleAdapters, \"claude\")", 1},
		{"no daemon", "cycleAdapters = nil", 0},
	} {
		src := "package core\nfunc TestX(t *testing.T) {\n" + c.body + "\n}\n"
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "x_test.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, late := lateCycleAdapterWrites(fset, file); len(late) != c.late {
			t.Errorf("%s: %q, want %d", c.name, late, c.late)
		}
	}
}

// lateCycleAdapterWrites returns the number of functions in file that start a test daemon and
// change cycleAdapters, and a message for each change after the start.
func lateCycleAdapterWrites(fset *token.FileSet, file *ast.File) (int, []string) {
	checked := 0
	var late []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		start := token.NoPos
		var writes []token.Pos
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if id, ok := n.Fun.(*ast.Ident); ok {
					switch {
					case id.Name == "startTestDaemon" && !start.IsValid():
						start = n.Pos()
					case id.Name == "delete" && len(n.Args) > 0 && isCycleAdapters(n.Args[0]):
						writes = append(writes, n.Pos())
					}
				}
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					if index, ok := lhs.(*ast.IndexExpr); ok {
						lhs = index.X
					}
					if isCycleAdapters(lhs) {
						writes = append(writes, n.Pos())
					}
				}
			}
			return true
		})
		if !start.IsValid() || len(writes) == 0 {
			continue
		}
		checked++
		for _, write := range writes {
			if write > start {
				late = append(late, fset.Position(write).String()+": "+fn.Name.Name+" changes cycleAdapters after startTestDaemon")
			}
		}
	}
	return checked, late
}

func isCycleAdapters(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "cycleAdapters"
}
