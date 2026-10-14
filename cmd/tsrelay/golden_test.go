package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// golden compares got with testdata/<name>.golden. With -update it rewrites
// that file and its ANSI-stripped twin <name>.txt, which is what to read
// in a diff.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join("testdata", name+".txt"), []byte(ansi.Strip(got)), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./cmd/tsrelay -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs; got (escapes stripped):\n%s\nwant:\n%s", path, ansi.Strip(got), ansi.Strip(string(want)))
	}
}
