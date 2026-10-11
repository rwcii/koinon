package contributor

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/setup"
)

// Use the daemon's admitted-family source, including command-only families, rather
// than a second hard-coded list that could silently omit a future family.
func TestAdmittedFamiliesHavePermissionGuidance(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "store.go", read(t, "internal/core/store.go"), 0)
	if err != nil {
		t.Fatal(err)
	}
	families := map[string]bool{}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "validKey" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			statement, ok := node.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			tag, ok := statement.Tag.(*ast.Ident)
			if !ok || tag.Name != "family" {
				return true
			}
			for _, clause := range statement.Body.List {
				for _, expression := range clause.(*ast.CaseClause).List {
					literal, ok := expression.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						t.Fatal("admitted-family source changed: update the guidance guard")
					}
					family, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatal(err)
					}
					families[family] = true
				}
			}
			return false
		})
	}
	if len(families) == 0 {
		t.Fatal("admitted-family source changed: update the guidance guard")
	}
	for family := range families {
		t.Run(family, func(t *testing.T) {
			var out bytes.Buffer
			if err := setup.Guide(family, &out); err != nil {
				t.Fatalf("admitted family has no guide: %v", err)
			}
			for _, required := range []string{"### Recommended permissions", "docs/PERMISSIONS.md", "authorized upgrade"} {
				if !strings.Contains(out.String(), required) {
					t.Errorf("admitted family %s guide omits %s", family, required)
				}
			}
			if strings.Count(out.String(), "### Recommended permissions") != 1 {
				t.Fatal("guide should contain only its own family's recommendations")
			}
		})
	}
}
