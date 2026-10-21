package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/negotiate"
	"github.com/Radiergummi/tsrelay/internal/sbom"
)

// nobody is identified but has no role; the attribution needs none.
var nobody = identify.User("phone", "nobody@example.com", 4)

// requireSBOM skips tests of the attribution in a build without the SBOM.
// CI's sbom job builds it and runs these tests.
func requireSBOM(t *testing.T) {
	t.Helper()
	if !sbom.Available() {
		t.Skip("this build has no SBOM; run 'make sbom'")
	}
}

// The entry document links the licenses only when there are some.
func TestEntryDocumentLinksTheLicenses(t *testing.T) {
	e := newEnv(t)
	var me Me
	e.call(nobody, "GET", "/", nil, &me)
	if _, ok := me.Links["licenses"]; ok != sbom.Available() {
		t.Errorf("links.licenses = %q, SBOM available: %v", me.Links["licenses"], sbom.Available())
	}
}

// A plain go build has no SBOM, and says so rather than serving nothing.
func TestAttributionWithoutSBOM(t *testing.T) {
	if sbom.Available() {
		t.Skip("this build has an SBOM")
	}
	e := newEnv(t)
	for _, path := range []string{"/licenses", "/notices.txt", "/sbom.cdx.json"} {
		if resp := e.call(nobody, "GET", path, nil, nil); resp.StatusCode != http.StatusNotFound || resp.Header.Get("Content-Type") != "application/problem+json" {
			t.Errorf("GET %s = %d %s, want a 404 problem", path, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
}

func TestLicenses(t *testing.T) {
	requireSBOM(t)
	e := newEnv(t)
	e.srv.Config.Handler = negotiate.Extensions([]string{"/sbom.cdx.json"}, e.srv.Config.Handler)
	var doc sbom.Document
	if resp := e.call(nobody, "GET", "/licenses", nil, &doc); resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("GET /licenses = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if len(doc.Components) == 0 || doc.Components[0].TextID == "" {
		t.Fatalf("licenses document = %+v", doc.Components[:min(len(doc.Components), 1)])
	}
	// A browser gets the web UI.
	if resp, body := e.get("/licenses", "text/html"); resp.Header.Get("Content-Type") != "text/html" || body != "the app" {
		t.Errorf("GET /licenses as HTML = %s %q", resp.Header.Get("Content-Type"), body)
	}
	if resp, _ := e.get("/licenses.json", "text/html"); resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("GET /licenses.json = %s", resp.Header.Get("Content-Type"))
	}
}

func TestLicenseText(t *testing.T) {
	requireSBOM(t)
	e := newEnv(t)
	var doc sbom.Document
	json.Unmarshal(sbom.ProjectedJSON(), &doc)
	id := doc.Components[0].TextID
	want, _ := sbom.Text(id)
	resp, body := e.get("/licenses/"+id, "")
	if resp.StatusCode != 200 || body != want || resp.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("GET /licenses/%s = %d %s %.40q", id, resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
	if resp, _ := e.get("/licenses/0123456789abcdef0123456789abcdef", ""); resp.StatusCode != http.StatusNotFound || resp.Header.Get("Content-Type") != "application/problem+json" {
		t.Errorf("unknown text = %d %s, want a 404 problem", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestNoticesAndSBOM(t *testing.T) {
	requireSBOM(t)
	e := newEnv(t)
	// Through the extension handling the server uses: .json must not be
	// stripped from /sbom.cdx.json.
	e.srv.Config.Handler = negotiate.Extensions([]string{"/sbom.cdx.json"}, e.srv.Config.Handler)
	for _, tc := range []struct{ path, contentType, contains string }{
		{"/notices.txt", "text/plain; charset=utf-8", "github.com/spf13/cobra"},
		{"/sbom.cdx.json", "application/vnd.cyclonedx+json; version=1.6", `"bomFormat": "CycloneDX"`},
	} {
		resp, body := e.get(tc.path, "")
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != tc.contentType || !strings.Contains(body, tc.contains) {
			t.Errorf("GET %s = %d %s, body has %q: %v", tc.path, resp.StatusCode, resp.Header.Get("Content-Type"), tc.contains, strings.Contains(body, tc.contains))
		}
	}
}

// The footer links the commit the relay was built from, when it is known.
func TestEntryDocumentNamesTheCommit(t *testing.T) {
	e := newEnv(t)
	if _, body := e.get("/", ""); strings.Contains(body, `"commit"`) {
		t.Errorf("entry document has a commit, but none is known: %s", body)
	}
	e.api.Commit = "0123456789abcdef"
	var me Me
	if e.call(nobody, "GET", "/", nil, &me); me.Commit != "0123456789abcdef" {
		t.Errorf("entry document = %+v", me)
	}
}

func TestAttributionNeedsNoRole(t *testing.T) {
	requireSBOM(t)
	e := newEnv(t)
	for _, path := range []string{"/licenses", "/notices.txt", "/sbom.cdx.json"} {
		if resp := e.call(nobody, "GET", path, nil, nil); resp.StatusCode != 200 {
			t.Errorf("GET %s without a role = %d", path, resp.StatusCode)
		}
	}
	e.fake.Fail(errors.New("not on the tailnet"))
	if resp := e.call(nobody, "GET", "/licenses", nil, nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /licenses unidentified = %d, want 403", resp.StatusCode)
	}
}
