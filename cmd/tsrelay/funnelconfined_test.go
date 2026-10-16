package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFunnelConfined enforces that only funnel.go may use tsnet's Funnel
// APIs, so the public surface stays in one reviewable place.
func TestFunnelConfined(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		rel, _ := filepath.Rel(root, path)
		if filepath.ToSlash(rel) == "cmd/tsrelay/funnel.go" {
			return nil
		}
		for _, banned := range []string{"ListenFunnel", "FunnelOnly"} {
			if strings.Contains(string(b), banned) {
				t.Errorf("%s refers to %s; only cmd/tsrelay/funnel.go may use Funnel", path, banned)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 10 {
		t.Fatalf("only checked %d files; is the module root right?", checked)
	}
}
