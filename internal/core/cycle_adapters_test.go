package core

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// A test that swaps cycleAdapters does so before startTestDaemon: the daemon's cycle loop
// reads the map, and t.Cleanup runs last in, first out, so a swap and its restore registered
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
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			start := token.NoPos
			var swaps []token.Pos
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "startTestDaemon" && !start.IsValid() {
						start = n.Pos()
					}
				case *ast.AssignStmt:
					for _, lhs := range n.Lhs {
						if id, ok := lhs.(*ast.Ident); ok && id.Name == "cycleAdapters" {
							swaps = append(swaps, n.Pos())
						}
					}
				}
				return true
			})
			if !start.IsValid() || len(swaps) == 0 {
				continue
			}
			checked++
			for _, swap := range swaps {
				if swap > start {
					t.Errorf("%s: %s assigns cycleAdapters after startTestDaemon", fset.Position(swap), fn.Name.Name)
				}
			}
		}
	}
	if checked < 4 {
		t.Fatalf("checked %d tests that start a daemon and swap cycleAdapters, want at least 4", checked)
	}
}
