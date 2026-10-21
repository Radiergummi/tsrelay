package problem

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"slices"
	"strconv"
	"testing"
)

// TestCodesDocumented checks that every error code has an entry in
// docs/errors.md and every entry names a code. A problem's type is the URL
// of its code's entry on the website, so an entry missing here is a type that
// leads nowhere.
func TestCodesDocumented(t *testing.T) {
	codes := errorCodes(t)

	doc, err := os.ReadFile("../../docs/errors.md")
	if err != nil {
		t.Fatal(err)
	}
	var documented []string
	for _, m := range regexp.MustCompile("(?m)^### `([a-z_]+)`$").FindAllSubmatch(doc, -1) {
		documented = append(documented, string(m[1]))
	}

	for _, code := range codes {
		if !slices.Contains(documented, code) {
			t.Errorf("code %q has no entry in docs/errors.md", code)
		}
	}
	for _, code := range documented {
		if !slices.Contains(codes, code) {
			t.Errorf("docs/errors.md documents %q, which is not a code", code)
		}
	}
}

// errorCodes returns the package's error codes: the values of the const
// block that declares them, found by one of its names so that no comment or
// naming pattern decides what counts.
func errorCodes(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "problem.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST || !declares(gen, "SenderForbidden") {
			continue
		}
		var codes []string
		for _, spec := range gen.Specs {
			for _, v := range spec.(*ast.ValueSpec).Values {
				code, err := strconv.Unquote(v.(*ast.BasicLit).Value)
				if err != nil {
					t.Fatal(err)
				}
				codes = append(codes, code)
			}
		}
		return codes
	}
	t.Fatal("problem.go declares no SenderForbidden")
	return nil
}

func declares(gen *ast.GenDecl, name string) bool {
	for _, spec := range gen.Specs {
		for _, ident := range spec.(*ast.ValueSpec).Names {
			if ident.Name == name {
				return true
			}
		}
	}
	return false
}
