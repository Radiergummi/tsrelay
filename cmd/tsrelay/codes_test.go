package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestCLICodesDocumented keeps the table of the CLI's own error codes in
// docs/cli.md in step with the codes the CLI uses, as scripts depend on
// them.
func TestCLICodesDocumented(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	var used []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range regexp.MustCompile(`Code: *"([a-z_]+)"`).FindAllSubmatch(src, -1) {
			if c := string(m[1]); c != "usage" && !slices.Contains(used, c) {
				used = append(used, c)
			}
		}
	}
	doc, err := os.ReadFile("../../docs/cli.md")
	if err != nil {
		t.Fatal(err)
	}
	var documented []string
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\| ").FindAllSubmatch(doc, -1) {
		documented = append(documented, string(m[1]))
	}
	for _, c := range used {
		if !slices.Contains(documented, c) {
			t.Errorf("code %q is not in docs/cli.md's table of the CLI's codes", c)
		}
	}
	for _, c := range documented {
		if !slices.Contains(used, c) && strings.Contains(c, "_") {
			t.Errorf("docs/cli.md documents %q, which the CLI doesn't use", c)
		}
	}
}
