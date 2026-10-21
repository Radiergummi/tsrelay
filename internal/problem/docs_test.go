package problem

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
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

// errorCodes returns the package's error codes: its string constants whose
// values are snake_case, as codes are and HeaderError and typeBase are not.
func errorCodes(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	isCode := regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)
	var codes []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				for _, v := range spec.(*ast.ValueSpec).Values {
					lit, ok := v.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					if s, _ := strconv.Unquote(lit.Value); isCode.MatchString(s) {
						codes = append(codes, s)
					}
				}
			}
		}
	}
	if len(codes) == 0 {
		t.Fatal("found no error codes")
	}
	return codes
}
