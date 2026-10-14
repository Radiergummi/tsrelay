package web

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// The built index.html must contain nothing the CSP blocks (DESIGN.md
// §5.7): no inline script, no style element, no style or event-handler
// attribute. Vite's output and our index.html template are both checked
// here, since a plugin could inject either.
func TestIndexIsInert(t *testing.T) {
	b, err := fs.ReadFile(Dist, "index.html")
	if err != nil {
		t.Skip("web/dist is not built (run `make ui`); skipping the index.html CSP check")
	}
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			src := false
			for _, a := range n.Attr {
				switch {
				case a.Key == "src":
					src = true
				case a.Key == "style", strings.HasPrefix(a.Key, "on"):
					t.Errorf("<%s> has a %s attribute", n.Data, a.Key)
				}
			}
			if n.Data == "script" && !src {
				t.Error("index.html has an inline <script>")
			}
			if n.Data == "style" {
				t.Error("index.html has a <style> element")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
}
