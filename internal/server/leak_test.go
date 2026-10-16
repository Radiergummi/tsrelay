package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/inspect"
)

// Secrets in query strings, header values and bodies must never reach a
// span (name, attributes, events, status, links) or /metrics, whether the
// call is delivered, refused, replayed or a test request (DESIGN.md §13).
func TestTelemetryLeaksNothing(t *testing.T) {
	secrets := []string{"SECRETQUERY", "SECRETHEADER", "SECRETBODY", "SECRETCOOKIE", "SECRETTESTBODY", "SECRETTESTHEADER"}
	e, rec := tracedEnv(t, Config{})
	app, _ := traceApp(t)
	h, _ := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb", Settings: hooks.Settings{Capture: new(true)}})
	defer e.cli(h.ID, app)()
	hdr := http.Header{
		"X-Api-Token": {"SECRETHEADER"},
		"Cookie":      {"session=SECRETCOOKIE"},
		"User-Agent":  {"SECRETHEADER-agent"},
	}

	// Delivered.
	resp := e.send(h, "/done?token=SECRETQUERY", []byte("SECRETBODY"), hdr)
	io.ReadAll(resp.Body)
	callID := resp.Header.Get("Tsrelay-Call-Id")
	// Refused: a node without the sender role.
	req, _ := http.NewRequest("POST", h.URL+"/x?token=SECRETQUERY", strings.NewReader("SECRETBODY"))
	req.Header = hdr.Clone()
	if r, err := e.fake.Client(receiver).Do(req); err == nil {
		r.Body.Close()
	}
	// Replayed.
	e.waitFor(func() bool {
		var c inspect.Call
		return e.getJSON("/hooks/"+h.ID+"/calls/"+callID, &c) == 200 && c.Outcome == inspect.OutcomeOK
	})
	e.postJSON("/hooks/"+h.ID+"/calls/"+callID+"/replay", nil, nil)
	// A test request through the API.
	e.postJSON("/hooks/"+h.ID+"/test", map[string]any{
		"method": "POST", "path": "/t?q=SECRETQUERY",
		"headers": map[string][]string{"X-Test": {"SECRETTESTHEADER"}},
		"body":    "SECRETTESTBODY",
	}, nil)
	e.waitFor(func() bool {
		n := 0
		for _, s := range rec.Ended() {
			if s.Name() == "tsrelay.call" {
				n++
			}
		}
		return n >= 4
	})

	var dump strings.Builder
	for _, s := range rec.Ended() {
		fmt.Fprintf(&dump, "%s %v %v %v %v\n", s.Name(), s.Attributes(), s.Events(), s.Status(), s.Links())
	}
	ops := &Ops{}
	ops.Ready(e.srv)
	m := httptest.NewRecorder()
	ops.ServeHTTP(m, httptest.NewRequest("GET", "/metrics", nil))
	dump.WriteString(m.Body.String())

	for _, secret := range secrets {
		if strings.Contains(dump.String(), secret) {
			t.Errorf("telemetry contains %q:\n%s", secret, dump.String())
		}
	}
}
