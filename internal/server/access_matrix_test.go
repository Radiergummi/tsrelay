package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/sbom"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// The identities of the access matrix. The owner receives on dev and prod,
// and may make prod hooks public.
var matrixIDs = map[string]authz.Identity{
	"owner": identify.User("owner-laptop", "owner@example.com", 1,
		`{"roles":["receiver"],"channels":["dev","prod"]}`, `{"roles":["receiver"],"channels":["prod"],"public":true}`),
	"other":        identify.User("other-desk", "other@example.com", 2, `{"roles":["receiver"],"channels":["dev"]}`),
	"adminAll":     identify.User("ops-box", "ops@example.com", 3, `{"roles":["admin"]}`),
	"adminProd":    identify.User("prod-ops-box", "prodops@example.com", 4, `{"roles":["admin"],"channels":["prod"]}`),
	"taggedAdmin":  identify.Tagged("ops-robot", []string{"tag:ops"}, `{"roles":["admin"]}`),
	"noRole":       identify.User("guest-box", "guest@example.com", 5),
	"ownerNoRoles": identify.User("owner-old-laptop", "owner@example.com", 1), // the owner, on a node that lost its roles
	"senderDev":    identify.Tagged("dev-worker", []string{"tag:dev"}, `{"roles":["sender"],"channels":["dev"]}`),
	"senderProd":   identify.Tagged("prod-worker", []string{"tag:prod"}, `{"roles":["sender"],"channels":["prod"]}`),
}

// statuses maps an identity to the status it gets; "" is everyone not named.
// Without "", the row is asked only as the identities named.
type statuses map[string]int

// How a row is asked.
const (
	asJSON     = ""
	asEvents   = "events"   // Accept: text/event-stream
	asHTML     = "html"     // as a browser, without following redirects
	asUpgrade  = "upgrade"  // a tunnel attach
	asCall     = "call"     // a sender's call: path is appended to the hook's tailnet URL
	asInternet = "internet" // a call through the Funnel listener: path is appended to the public route
)

type matrixRow struct {
	route  string // the pattern in Server.Routes() the row exercises, or "call"
	path   string // {dev}, {prod}, {nope}, {devCall}, {prodCall}, {tmpDev}, {tmpProd} and {textId} are filled in
	kind   string
	body   string
	header http.Header
	want   statuses
	// see, for hook listings, names the fixture hooks (dev, prod) each
	// identity must find in the answer (sorted), and no others.
	see map[string][]string
}

// The fixture calls' body, and how call JSON carries it.
const secretBody = `{"secret":"s3cr3t-body"}`

var secretBodyJSON = base64.StdEncoding.EncodeToString([]byte(secretBody))

// mayHoldOwnerData are the identities that may see the hooks' owner-only
// data: the owner, on either node.
var mayHoldOwnerData = []string{"owner", "ownerNoRoles"}

// Every route the API and UI serve, as every identity, through the real
// server: who may see, change, attach to and call which hook (DESIGN.md §5).
// A route without a row fails the test, so a new endpoint cannot skip the
// access rules unnoticed.
func TestAccessMatrix(t *testing.T) {
	e := newEnv(t, Config{PublicBaseURL: "https://tsrelay.example.ts.net"})
	owner := matrixIDs["owner"]
	e.api = &client.API{BaseURL: e.base, HTTP: tunnel.NewHTTPClient(e.fake.Dial(owner))}
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	t.Cleanup(local.Close)
	capture := hooks.Settings{Capture: new(true)}
	dev := e.hook(hooks.CreateRequest{Path: "dev", Channel: "dev", Settings: capture})
	prod := e.hook(hooks.CreateRequest{Path: "prod", Channel: "prod", Public: true, Settings: capture})
	t.Cleanup(e.cli(dev.ID, local.URL))
	t.Cleanup(e.cli(prod.ID, local.URL))

	// One delivered call per hook, carrying data only the owner may see.
	secret := http.Header{"X-Secret": {"s3cr3t-header"}}
	call := func(as string, h api.Hook) string {
		req, _ := http.NewRequest("POST", h.URL+"/done", strings.NewReader(secretBody))
		req.Header = secret.Clone()
		resp, err := e.fake.Client(matrixIDs[as]).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		id := resp.Header.Get("Tsrelay-Call-Id")
		e.waitFor(func() bool { c, ok := e.srv.inspect.Get(id); return ok && c.Outcome == inspect.OutcomeOK })
		return id
	}
	devCall, prodCall := call("senderDev", dev), call("senderProd", prod)
	var doc sbom.Document
	json.Unmarshal(sbom.ProjectedJSON(), &doc)

	rows := []matrixRow{
		{route: "GET /{$}", path: "/", want: statuses{"": 200}},
		{route: "GET /hooks", path: "/hooks", want: statuses{"": 200},
			see: map[string][]string{"owner": {"dev", "prod"}, "ownerNoRoles": {"dev", "prod"}}},
		// ?all=true is an admin's view of their channels; anyone else gets their own.
		{route: "GET /hooks", path: "/hooks?all=true", want: statuses{"": 200},
			see: map[string][]string{"owner": {"dev", "prod"}, "ownerNoRoles": {"dev", "prod"}, "adminAll": {"dev", "prod"}, "adminProd": {"prod"}}},
		{route: "GET /hooks", path: "/hooks", kind: asEvents, want: statuses{"": 403, "owner": 200, "other": 200, "adminAll": 200, "adminProd": 200}},
		{route: "GET /hooks", path: "/hooks?all=true", kind: asEvents, want: statuses{"": 403, "owner": 200, "other": 200, "adminAll": 200, "adminProd": 200}},

		{route: "POST /hooks", path: "/hooks", body: `{"channel":"dev"}`, want: statuses{"": 403, "owner": 201, "other": 201}},
		{route: "POST /hooks", path: "/hooks", body: `{"channel":"prod"}`, want: statuses{"other": 422}},
		{route: "POST /hooks", path: "/hooks", body: `{"channel":"dev","public":true}`, want: statuses{"owner": 422}}, // public is scoped to prod

		{route: "GET /hooks/{id}", path: "/hooks/{dev}", want: statuses{"": 404, "owner": 200, "ownerNoRoles": 200, "adminAll": 200}},
		{route: "GET /hooks/{id}", path: "/hooks/{prod}", want: statuses{"": 404, "owner": 200, "ownerNoRoles": 200, "adminAll": 200, "adminProd": 200}},
		{route: "GET /hooks/{id}", path: "/hooks/{nope}", want: statuses{"": 404}},
		{route: "GET /hooks/{id}", path: "/hooks/{dev}", kind: asEvents, want: statuses{"": 404, "owner": 200, "ownerNoRoles": 403, "adminAll": 200}},
		{route: "GET /hooks/{id}", path: "/hooks/{prod}", kind: asEvents, want: statuses{"": 404, "owner": 200, "ownerNoRoles": 403, "adminAll": 200, "adminProd": 200}},
		{route: "GET /hooks/{id}", path: "/hooks/{nope}", kind: asEvents, want: statuses{"": 404}},

		{route: "PATCH /hooks/{id}", path: "/hooks/{dev}", body: `{"description":"edited"}`, want: statuses{"": 404, "owner": 200, "ownerNoRoles": 403, "adminAll": 403}},
		{route: "PATCH /hooks/{id}", path: "/hooks/{prod}", body: `{"description":"edited"}`, want: statuses{"": 404, "owner": 200, "ownerNoRoles": 403, "adminAll": 403, "adminProd": 403}},
		{route: "PATCH /hooks/{id}", path: "/hooks/{nope}", body: `{"description":"edited"}`, want: statuses{"": 404}},
		{route: "PATCH /hooks/{id}", path: "/hooks/{dev}", body: `{"channel":"staging"}`, want: statuses{"owner": 422}},

		{route: "POST /hooks/{id}/rotate-public-url", path: "/hooks/{dev}/rotate-public-url", want: statuses{"": 404, "owner": 409, "ownerNoRoles": 403, "adminAll": 403}},
		{route: "POST /hooks/{id}/rotate-public-url", path: "/hooks/{prod}/rotate-public-url", want: statuses{"": 404, "owner": 200, "ownerNoRoles": 403, "adminAll": 403, "adminProd": 403}},
		{route: "POST /hooks/{id}/rotate-public-url", path: "/hooks/{nope}/rotate-public-url", want: statuses{"": 404}},

		// A delete needs no role, only sight of the hook.
		{route: "DELETE /hooks/{id}", path: "/hooks/{tmpDev}", want: statuses{"": 404, "owner": 204, "ownerNoRoles": 204, "adminAll": 204}},
		{route: "DELETE /hooks/{id}", path: "/hooks/{tmpProd}", want: statuses{"": 404, "owner": 204, "ownerNoRoles": 204, "adminAll": 204, "adminProd": 204}},
		{route: "DELETE /hooks/{id}", path: "/hooks/{nope}", want: statuses{"": 404}},

		{route: "GET /hooks/{id}/calls", path: "/hooks/{dev}/calls", want: statuses{"": 404, "owner": 200, "ownerNoRoles": 200, "adminAll": 200}},
		{route: "GET /hooks/{id}/calls", path: "/hooks/{prod}/calls", want: statuses{"": 404, "owner": 200, "ownerNoRoles": 200, "adminAll": 200, "adminProd": 200}},
		{route: "GET /hooks/{id}/calls", path: "/hooks/{nope}/calls", want: statuses{"": 404}},
		{route: "GET /hooks/{id}/calls", path: "/hooks/{dev}/calls", kind: asEvents, want: statuses{"": 404, "owner": 200, "ownerNoRoles": 403, "adminAll": 200}},
		{route: "GET /hooks/{id}/calls", path: "/hooks/{prod}/calls", kind: asEvents, want: statuses{"": 404, "owner": 200, "ownerNoRoles": 403, "adminAll": 200, "adminProd": 200}},
		{route: "GET /hooks/{id}/calls", path: "/hooks/{nope}/calls", kind: asEvents, want: statuses{"": 404}},

		{route: "GET /hooks/{id}/calls/{callID}", path: "/hooks/{dev}/calls/{devCall}", want: statuses{"": 404, "owner": 200, "ownerNoRoles": 200, "adminAll": 200}},
		{route: "GET /hooks/{id}/calls/{callID}", path: "/hooks/{prod}/calls/{prodCall}", want: statuses{"": 404, "owner": 200, "ownerNoRoles": 200, "adminAll": 200, "adminProd": 200}},
		{route: "GET /hooks/{id}/calls/{callID}", path: "/hooks/{nope}/calls/{devCall}", want: statuses{"": 404}},
		{route: "GET /hooks/{id}/calls/{callID}", path: "/hooks/{prod}/calls/{devCall}", want: statuses{"owner": 404, "adminAll": 404}}, // another hook's call

		{route: "POST /hooks/{id}/calls/{callID}/replay", path: "/hooks/{dev}/calls/{devCall}/replay", want: statuses{"": 404, "owner": 201, "ownerNoRoles": 403, "adminAll": 403}},
		{route: "POST /hooks/{id}/calls/{callID}/replay", path: "/hooks/{prod}/calls/{prodCall}/replay", want: statuses{"": 404, "owner": 201, "ownerNoRoles": 403, "adminAll": 403, "adminProd": 403}},
		{route: "POST /hooks/{id}/calls/{callID}/replay", path: "/hooks/{nope}/calls/{devCall}/replay", want: statuses{"": 404}},

		{route: "POST /hooks/{id}/test", path: "/hooks/{dev}/test", body: `{}`, want: statuses{"": 404, "owner": 201, "ownerNoRoles": 403, "adminAll": 403}},
		{route: "POST /hooks/{id}/test", path: "/hooks/{prod}/test", body: `{}`, want: statuses{"": 404, "owner": 201, "ownerNoRoles": 403, "adminAll": 403, "adminProd": 403}},
		{route: "POST /hooks/{id}/test", path: "/hooks/{nope}/test", body: `{}`, want: statuses{"": 404}},

		{route: "GET /channels", path: "/channels", want: statuses{"": 200}},
		{route: "GET /channels", path: "/channels?all=true", want: statuses{"": 200}},
		{route: "GET /channels/{name}", path: "/channels/dev", want: statuses{"": 200}},
		{route: "GET /channels/{name}", path: "/channels/prod?all=true", want: statuses{"": 200}},
		{route: "GET /channels/{name}", path: "/channels/Not_A_Channel", want: statuses{"": 404}},

		// Another user's hook does not exist for the caller; a node without
		// the receiver role is refused before any hook is looked up.
		{route: "GET /tunnel", path: "/tunnel?hook={tmpDev}", kind: asUpgrade, want: statuses{"": 403, "owner": 101, "other": 404}},
		{route: "GET /tunnel", path: "/tunnel?hook={tmpProd}", kind: asUpgrade, want: statuses{"": 403, "owner": 101, "other": 404}},
		{route: "GET /tunnel", path: "/tunnel?hook={nope}", kind: asUpgrade, want: statuses{"": 403, "owner": 404, "other": 404}},

		// The documents and attribution need no role.
		{route: "GET /openapi", path: "/openapi.json", want: statuses{"": 200}},
		{route: "GET /openapi.yaml", path: "/openapi.yaml", want: statuses{"": 200}},
		{route: "GET /licenses", path: "/licenses", want: statuses{"": 200}},
		{route: "GET /licenses/{textId}", path: "/licenses/{textId}", want: statuses{"": 200}},
		{route: "GET /notices.txt", path: "/notices.txt", want: statuses{"": 200}},
		{route: "GET /sbom.cdx.json", path: "/sbom.cdx.json", want: statuses{"": 200}},

		{route: "GET /inspect/{callID}", path: "/inspect/{devCall}", kind: asHTML, want: statuses{"": 404, "owner": 303, "ownerNoRoles": 303, "adminAll": 303}},
		{route: "GET /inspect/{callID}", path: "/inspect/{prodCall}", kind: asHTML, want: statuses{"": 404, "owner": 303, "ownerNoRoles": 303, "adminAll": 303, "adminProd": 303}},
		{route: "GET /inspect/{callID}", path: "/inspect/c_nope", kind: asHTML, want: statuses{"": 404}},

		// Calls: only a sender role reaches a hook, and only on its channel.
		{route: "call", path: "{dev}/x", kind: asCall, want: statuses{"": 403, "senderDev": 202, "senderProd": 404}},
		{route: "call", path: "{prod}/x", kind: asCall, want: statuses{"": 403, "senderDev": 404, "senderProd": 202}},
		// Headers that look like the relay's own cannot make a call a replay,
		// which would skip the sender check.
		{route: "call", path: "{dev}/x", kind: asCall, want: statuses{"senderProd": 404}, header: http.Header{
			"Tsrelay-Sender-Node": {"owner-laptop"}, "Tsrelay-Sender-Login": {"owner@example.com"},
			"Tsrelay-Hook": {dev.ID}, "Tsrelay-Call-Id": {devCall}, "Tsrelay-Replay-Of": {devCall}, "Tsrelay-Kind": {"replay"},
		}},
		{route: "call", path: "/x", kind: asInternet, want: statuses{"senderDev": 202}}, // the identity is ignored: the internet has none
	}

	m := &matrix{e: e, dev: dev.ID, prod: prod.ID, devCall: devCall, prodCall: prodCall, textID: doc.Components[0].TextID, public: publicServer(t, e)}
	// The owner sees what the rows below must show nobody else.
	for path, want := range map[string][]string{
		"/hooks/" + dev.ID + "/calls/" + devCall: {secretBodyJSON, "s3cr3t-header"},
		"/hooks/" + prod.ID:                      {`"publicUrl":"`},
	} {
		_, body := m.ask(t, owner, matrixRow{route: "GET"}, path)
		for _, s := range want {
			if !strings.Contains(body, s) {
				t.Fatalf("precondition: the owner's %s lacks %q: %s", path, s, body)
			}
		}
	}
	covered := map[string]bool{}
	for _, row := range rows {
		covered[row.route] = true
		for _, who := range slices.Sorted(maps.Keys(matrixIDs)) {
			want, ok := row.want[who]
			if !ok {
				if want, ok = row.want[""]; !ok {
					continue
				}
			}
			m.check(t, row, who, want)
		}
	}
	var missing []string
	for _, r := range e.srv.Routes() {
		if !covered[r] {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		t.Errorf("routes without rows in the access matrix: %v", missing)
	}
}

type matrix struct {
	e                 *env
	dev, prod         string
	devCall, prodCall string
	textID            string
	public            *httptest.Server
}

// check asks row as who and compares the answer with want.
func (m *matrix) check(t *testing.T, row matrixRow, who string, want int) {
	t.Helper()
	id := matrixIDs[who]
	path, cleanup := m.fill(t, row.path)
	defer cleanup()
	got, body := m.ask(t, id, row, path)
	name := strings.Join([]string{who, strings.Fields(row.route)[0], row.path, row.kind}, " ")
	if got != want {
		t.Errorf("%s = %d, want %d: %s", name, got, want, firstLine(body))
		return
	}
	if got < 200 || got >= 300 || row.kind != asJSON {
		return
	}
	if !slices.Contains(mayHoldOwnerData, who) {
		prod, _ := m.e.srv.hooks.Get(hooks.ID(m.prod))
		for _, s := range []string{secretBodyJSON, "s3cr3t", `"publicUrl":"`, prod.PublicToken} {
			if strings.Contains(body, s) {
				t.Errorf("%s carries owner-only data %q: %s", name, s, body)
			}
		}
	}
	if row.see != nil {
		seen := row.see[who]
		var list []api.Hook
		json.Unmarshal([]byte(body), &list)
		var found []string
		for _, h := range list {
			switch h.ID {
			case m.dev:
				found = append(found, "dev")
			case m.prod:
				found = append(found, "prod")
			}
		}
		slices.Sort(found)
		if !slices.Equal(found, seen) {
			t.Errorf("%s lists %v, want %v", name, found, seen)
		}
	}
}

// fill replaces the placeholders in path. {tmpDev} and {tmpProd} are fresh
// hooks of the owner, like dev and prod, for rows that delete or attach;
// cleanup removes them.
func (m *matrix) fill(t *testing.T, path string) (string, func()) {
	t.Helper()
	cleanup := func() {}
	for key, req := range map[string]hooks.CreateRequest{"{tmpDev}": {Channel: "dev"}, "{tmpProd}": {Channel: "prod", Public: true}} {
		if !strings.Contains(path, key) {
			continue
		}
		owner := matrixIDs["owner"]
		h, err := m.e.srv.tunnels.Create(context.Background(), owner, req)
		if err != nil {
			t.Fatal(err)
		}
		path = strings.ReplaceAll(path, key, string(h.ID))
		cleanup = func() { m.e.srv.tunnels.Delete(context.Background(), owner, h.ID) }
	}
	hookURL := func(id string) string {
		h, _ := m.e.srv.hooks.Get(hooks.ID(id))
		return m.e.base + h.Route()
	}
	r := strings.NewReplacer("{devCall}", m.devCall, "{prodCall}", m.prodCall, "{nope}", "h_nope", "{textId}", m.textID)
	path = r.Replace(path)
	if strings.HasPrefix(path, "{dev}") || strings.HasPrefix(path, "{prod}") {
		path = strings.NewReplacer("{dev}", hookURL(m.dev), "{prod}", hookURL(m.prod)).Replace(path)
	}
	return strings.NewReplacer("{dev}", m.dev, "{prod}", m.prod).Replace(path), cleanup
}

// ask sends row's request as id and returns the status and body.
func (m *matrix) ask(t *testing.T, id authz.Identity, row matrixRow, path string) (int, string) {
	t.Helper()
	method := strings.Fields(row.route)[0]
	url := m.e.base + path
	switch row.kind {
	case asCall:
		method, url = "POST", path
	case asInternet:
		prod, _ := m.e.srv.hooks.Get(hooks.ID(m.prod))
		resp := publicGet(t, m.public, "tsrelay.example.ts.net", prod.PublicRoute()+path)
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var body io.Reader
	if row.body != "" {
		body = strings.NewReader(row.body)
	}
	req, _ := http.NewRequestWithContext(ctx, method, url, body)
	for k, v := range row.header {
		req.Header[k] = v
	}
	switch row.kind {
	case asJSON:
		req.Header.Set("Accept", "application/json")
		if method != "GET" {
			req.Header.Set("Content-Type", "application/json")
		}
	case asEvents:
		req.Header.Set("Accept", "text/event-stream")
	case asHTML:
		req.Header.Set("Accept", "text/html")
	case asUpgrade:
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", tunnel.UpgradeToken)
	}
	c := m.e.fake.Client(id)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if row.kind == asEvents || resp.StatusCode == http.StatusSwitchingProtocols {
		return resp.StatusCode, "" // an open stream or tunnel; its headers are the answer
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}
