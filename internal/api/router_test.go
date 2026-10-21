package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/sbom"
)

// send sends method path as moritz with the given Accept header, and
// writes as JSON.
func (e *env) send(method, path, accept string) (*http.Response, string) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if method != "GET" && method != "HEAD" {
		req.Header.Set("Content-Type", "application/json")
	}
	c := e.fake.Client(moritz)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// A method no route serves at a path another method's route serves is
// refused with the methods that are.
func TestMethodNotAllowed(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Path: "cb"}, &h)
	for path, allow := range map[string]string{
		"PUT /hooks":          "GET, HEAD, POST",
		"POST /hooks/" + h.ID: "GET, HEAD, PATCH, DELETE",
		"PATCH /channels":     "GET, HEAD",
		"DELETE /hooks.json":  "GET, HEAD, POST",
		// The web UI answers GET everywhere, so Allow names it here too.
		"PUT /hooks/" + h.ID + "/test": "GET, HEAD, POST",
	} {
		method, p, _ := strings.Cut(path, " ")
		resp, _ := e.send(method, p, "")
		if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != allow || resp.Header.Get(problem.HeaderError) != problem.MethodNotAllowed {
			t.Errorf("%s = %d Allow %q error %q, want 405 Allow %q", path, resp.StatusCode, resp.Header.Get("Allow"), resp.Header.Get(problem.HeaderError), allow)
		}
	}
}

// GET and HEAD belong to the app wherever no route claims them, so they
// are never 405: they reach the fallback, whose answers the access matrix
// checks with the real web UI.
func TestGetNeverMethodNotAllowed(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Path: "cb"}, &h)
	if resp, _ := e.send("HEAD", "/hooks", ""); resp.StatusCode != 200 {
		t.Errorf("HEAD /hooks = %d", resp.StatusCode)
	}
	for _, method := range []string{"GET", "HEAD"} {
		if resp, body := e.send(method, "/hooks/"+h.ID+"/test", "text/html"); resp.StatusCode != 200 || method == "GET" && body != "the app" {
			t.Errorf("%s test = %d %q, want the fallback", method, resp.StatusCode, body)
		}
	}
}

// An extension on an inner segment of a path no route claims names no
// resource, whatever the client accepts. (Where a route's wildcard takes
// the segment, as /hooks/{id}/calls does, the route decides.)
func TestMidPathSuffixIsNotFound(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/hooks.json/calls", "/channels.html/x/y"} {
		for _, accept := range []string{"", "text/html"} {
			resp, _ := e.send("GET", p, accept)
			if resp.StatusCode != 404 || resp.Header.Get("Content-Type") != "application/problem+json" {
				t.Errorf("GET %s (%q) = %d %s, want a 404 problem", p, accept, resp.StatusCode, resp.Header.Get("Content-Type"))
			}
		}
	}
}

func TestDottedChannelWithSuffix(t *testing.T) {
	e := newEnv(t)
	resp, body := e.send("GET", "/channels/prod.payments.json", "text/html")
	var c Channel
	json.Unmarshal([]byte(body), &c)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json" || c.Name != "prod.payments" {
		t.Fatalf("GET /channels/prod.payments.json = %d %s %s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
}

// A document with one type refuses an extension asking for another,
// rather than serving itself under the wrong name.
func TestFixedTypeDocumentsRefuseOtherSuffixes(t *testing.T) {
	e := newEnv(t)
	refused := []string{"/install.sh.json", "/install.ps1.html", "/download/linux/amd64.json", "/openapi.html", "/openapi.yaml.json"}
	if sbom.Available() {
		refused = append(refused, "/notices.txt.json", "/sbom.cdx.json.html")
		// The SBOM is JSON, so asking for JSON by name is no refusal.
		if resp, _ := e.send("GET", "/sbom.cdx.json.json", ""); resp.StatusCode != 200 {
			t.Errorf("GET /sbom.cdx.json.json = %d, want 200", resp.StatusCode)
		}
	}
	for _, p := range refused {
		if resp, _ := e.send("GET", p, ""); resp.StatusCode != http.StatusNotAcceptable {
			t.Errorf("GET %s = %d, want 406", p, resp.StatusCode)
		}
	}
	for p, ct := range map[string]string{
		"/openapi.json": "application/json",
		"/openapi":      "application/json",
		"/openapi.yaml": "application/yaml",
		"/install.sh":   "text/x-shellscript; charset=utf-8",
		"/install.ps1":  "text/plain; charset=utf-8",
	} {
		if resp, _ := e.send("GET", p, ""); resp.StatusCode != 200 || resp.Header.Get("Content-Type") != ct {
			t.Errorf("GET %s = %d %s, want 200 %s", p, resp.StatusCode, resp.Header.Get("Content-Type"), ct)
		}
	}
}

// A document that needs the caller identifies them before it looks at the
// extension, as the bare path does.
func TestDocumentsIdentifyBeforeRefusing(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/notices.txt", "/notices.txt.json", "/licenses/0123456789abcdef0123456789abcdef.json"} {
		resp, err := http.Get(e.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s unidentified = %d, want 403", p, resp.StatusCode)
		}
	}
}
