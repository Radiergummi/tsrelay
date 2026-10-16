package api

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/negotiate"
)

// get GETs path as id with the given Accept header, through the same
// extension handling the server uses.
func (e *env) get(path, accept string) (*http.Response, string) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
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

func TestResourcesAreNegotiated(t *testing.T) {
	e := newEnv(t)
	e.srv.Config.Handler = negotiate.Extensions(e.srv.Config.Handler)
	var h Hook
	e.call(moritz, "POST", "/hooks", hooks.CreateRequest{Path: "cb"}, &h)
	const browser = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	for _, path := range []string{"/hooks", "/hooks/" + h.ID, "/hooks/" + h.ID + "/calls"} {
		for _, tc := range []struct {
			name, path, accept, wantType string
		}{
			{"default", path, "", "application/json"},
			{"Accept JSON", path, "application/json", "application/json"},
			{"versioned alias", path, "application/vnd.tsrelay.v1+json", "application/json"},
			{"browser", path, browser, "text/html"},
			{".json", path + ".json", browser, "application/json"},
			{".html", path + ".html", "application/json", "text/html"},
			{"unacceptable", path, "image/png", "application/problem+json"},
		} {
			resp, body := e.get(tc.path, tc.accept)
			if got := resp.Header.Get("Content-Type"); got != tc.wantType {
				t.Errorf("%s %s: Content-Type = %s, want %s (%d %s)", tc.path, tc.name, got, tc.wantType, resp.StatusCode, body)
			}
			if tc.name == "unacceptable" && resp.StatusCode != http.StatusNotAcceptable {
				t.Errorf("%s %s = %d, want 406", tc.path, tc.name, resp.StatusCode)
			}
			if !slices.Contains(resp.Header.Values("Vary"), "Accept") {
				t.Errorf("%s %s: no Vary: Accept", tc.path, tc.name)
			}
		}
	}
	// A single call has no stream.
	if resp, _ := e.get("/hooks/"+h.ID+"/calls/c_1", "text/event-stream"); resp.StatusCode != http.StatusNotAcceptable {
		t.Errorf("a call's stream = %d, want 406", resp.StatusCode)
	}
}

func TestEntryDocument(t *testing.T) {
	e := newEnv(t)
	resp, body := e.get("/", "")
	var me Me
	if err := json.Unmarshal([]byte(body), &me); err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET / = %d %s", resp.StatusCode, body)
	}
	if me.Login != "moritz@example.com" || me.Links["hooks"] != "/hooks" || me.Links["tunnel"] != "/tunnel" || me.Links["openapi"] != "/openapi.json" {
		t.Errorf("entry document = %+v", me)
	}
	if resp, _ := e.get("/", "text/html"); resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/hooks" {
		t.Errorf("GET / as HTML = %d → %q, want a redirect to /hooks", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestOpenAPIDocument(t *testing.T) {
	e := newEnv(t)
	resp, body := e.get("/openapi", "")
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil || resp.Header.Get("Content-Type") != "application/json" || doc["openapi"] != "3.1.0" {
		t.Fatalf("GET /openapi = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp, body = e.get("/openapi.yaml", "")
	if resp.Header.Get("Content-Type") != "application/yaml" || !strings.HasPrefix(body, "openapi: 3.1.0") {
		t.Errorf("GET /openapi.yaml = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

// The OpenAPI document describes exactly the routes Register mounts.
func TestOpenAPIMatchesRoutes(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(openAPIYAML, &doc); err != nil {
		t.Fatal(err)
	}
	var documented []string
	for path, item := range doc.Paths {
		for method := range item {
			if method != "parameters" {
				documented = append(documented, strings.ToUpper(method)+" "+path)
			}
		}
	}
	a := &API{}
	a.Register(http.NewServeMux())
	var registered []string
	for _, r := range a.Routes() {
		registered = append(registered, strings.TrimSuffix(r, "{$}"))
	}
	slices.Sort(documented)
	slices.Sort(registered)
	for _, r := range registered {
		if !slices.Contains(documented, r) {
			t.Errorf("%s is not in openapi.yaml", r)
		}
	}
	for _, d := range documented {
		if !slices.Contains(registered, d) {
			t.Errorf("openapi.yaml documents %s, which is not registered", d)
		}
	}
}
