package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/Radiergummi/tsrelay/internal/tunnel/tunneltest"
)

// seedCall records a finished call with owner-only data for moritz's hook.
func (e *env) seedCall(id, hookID string, body inspect.Capture) inspect.Call {
	c := inspect.Call{
		ID: id, HookID: hooks.ID(hookID), Owner: moritz.UserID, ReceivedAt: time.Now(), Kind: inspect.KindSender,
		Sender: inspect.Sender{Node: "worker", Tags: []string{"tag:prod-worker"}},
		Method: "POST", Path: "/done", Query: "token=s3cr3t",
		RequestHeaders: http.Header{"X-Custom": {"secret-header"}}, RequestBody: body,
		Outcome: inspect.OutcomeOK, Status: 200,
		Attempts: []inspect.Attempt{{ID: "a_1", Status: 200,
			ResponseHeaders: http.Header{"X-Reply": {"secret-reply"}},
			ResponseBody:    inspect.Capture{Body: []byte("secret reply"), Size: 12, Captured: true}}},
	}
	e.in.Add(c)
	return c
}

func completeBody(s string) inspect.Capture {
	return inspect.Capture{Body: []byte(s), Size: int64(len(s)), Captured: true}
}

func TestCallVisibility(t *testing.T) {
	e := newEnv(t)
	var h, h2 Hook
	e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "cb"}, &h)
	e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "cb2"}, &h2)
	e.seedCall("c_1", h.ID, completeBody(`{"secret":true}`))

	var list []inspect.Summary
	if resp := e.call(moritz, "GET", "/api/v1/hooks/"+h.ID+"/calls", nil, &list); resp.StatusCode != 200 || len(list) != 1 || list[0].ID != "c_1" {
		t.Fatalf("owner list = %d %+v", resp.StatusCode, list)
	}
	var full inspect.Call
	if resp := e.call(moritz, "GET", "/api/v1/hooks/"+h.ID+"/calls/c_1", nil, &full); resp.StatusCode != 200 ||
		full.Query != "token=s3cr3t" || string(full.RequestBody.Body) != `{"secret":true}` || full.Attempts[0].ResponseHeaders.Get("X-Reply") == "" {
		t.Fatalf("owner detail = %d %+v", resp.StatusCode, full)
	}

	// Admins see metadata only.
	list = nil
	if resp := e.call(admin, "GET", "/api/v1/hooks/"+h.ID+"/calls", nil, &list); resp.StatusCode != 200 || len(list) != 1 {
		t.Fatalf("admin list = %d %+v", resp.StatusCode, list)
	}
	var meta inspect.Call
	if resp := e.call(admin, "GET", "/api/v1/hooks/"+h.ID+"/calls/c_1", nil, &meta); resp.StatusCode != 200 {
		t.Fatalf("admin detail = %d", resp.StatusCode)
	}
	if meta.Query != "" || meta.RequestHeaders != nil || meta.RequestBody.Body != nil ||
		meta.Attempts[0].ResponseHeaders != nil || meta.Attempts[0].ResponseBody.Body != nil {
		t.Fatalf("admin saw owner-only data: %+v", meta)
	}
	if meta.Status != 200 || meta.RequestBody.Size != 15 || meta.Path != "/done" {
		t.Fatalf("admin lost metadata: %+v", meta)
	}

	// Everyone else sees nothing, and a call is only found under its own hook.
	for _, path := range []string{"/api/v1/hooks/" + h.ID + "/calls", "/api/v1/hooks/" + h.ID + "/calls/c_1"} {
		if resp := e.call(other, "GET", path, nil, nil); resp.StatusCode != 404 {
			t.Errorf("other user GET %s = %d, want 404", path, resp.StatusCode)
		}
	}
	if resp := e.call(moritz, "GET", "/api/v1/hooks/"+h2.ID+"/calls/c_1", nil, nil); resp.StatusCode != 404 {
		t.Errorf("call under another hook = %d, want 404", resp.StatusCode)
	}
}

func TestDeleteForgetsCalls(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "cb"}, &h)
	e.seedCall("c_1", h.ID, completeBody("{}"))
	if resp := e.call(moritz, "DELETE", "/api/v1/hooks/"+h.ID, nil, nil); resp.StatusCode != 204 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if _, ok := e.in.Get("c_1"); ok {
		t.Fatal("the deleted hook's calls are still kept")
	}
}

// serve attaches a tunnel to hookID whose local app answers 200 "pong".
func (e *env) serve(hookID string) {
	tun, _ := tunneltest.Pair(e.t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		io.WriteString(w, "pong")
	}), tunnel.Options{}, nil)
	e.reg.Attach(tun, []string{hookID})
}

func TestReplayRules(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "cb"}, &h)
	e.serve(h.ID)
	e.seedCall("c_full", h.ID, completeBody(`{"n":1}`))
	e.seedCall("c_cut", h.ID, inspect.Capture{Body: []byte(`{"n":`), Size: 4096, Truncated: true, Captured: true})
	e.in.Add(inspect.Call{
		ID: "c_refused", HookID: hooks.ID(h.ID), Owner: moritz.UserID, Kind: inspect.KindSender,
		Method: "POST", Path: "/done", Outcome: problem.HookNotFound, Status: http.StatusNotFound,
		Attempts: []inspect.Attempt{},
	})
	long := e.seedCall("c_long", h.ID, completeBody(`{}`))
	e.in.Update(long.ID, func(c *inspect.Call) { c.QueryTruncated = true })
	replay := func(id authz.Identity, callID string) int {
		return e.call(id, "POST", "/api/v1/hooks/"+h.ID+"/calls/"+callID+"/replay", nil, nil).StatusCode
	}
	if code := replay(moritz, "c_cut"); code != 409 {
		t.Errorf("replay of a truncated body = %d, want 409", code)
	}
	if code := replay(moritz, "c_refused"); code != 409 {
		t.Errorf("replay of a call refused by channel = %d, want 409", code)
	}
	if code := replay(moritz, "c_long"); code != 409 {
		t.Errorf("replay of a truncated query = %d, want 409", code)
	}
	if code := replay(admin, "c_full"); code != 403 {
		t.Errorf("admin replay = %d, want 403", code)
	}
	if code := replay(other, "c_full"); code != 404 {
		t.Errorf("other user's replay = %d, want 404", code)
	}
	if code := replay(moritz, "c_nope"); code != 404 {
		t.Errorf("replay of an unknown call = %d, want 404", code)
	}
	var c inspect.Call
	if resp := e.call(moritz, "POST", "/api/v1/hooks/"+h.ID+"/calls/c_full/replay", nil, &c); resp.StatusCode != 201 ||
		c.Kind != inspect.KindReplay || c.ReplayOf != "c_full" || c.Outcome != inspect.OutcomeOK || c.Path != "/done" || c.Query != "token=s3cr3t" {
		t.Fatalf("replay = %d %+v", resp.StatusCode, c)
	}
}

func TestTestRequest(t *testing.T) {
	e := newEnv(t)
	var h Hook
	e.call(moritz, "POST", "/api/v1/hooks", hooks.CreateRequest{Path: "cb"}, &h)
	e.serve(h.ID)
	var c inspect.Call
	resp := e.call(moritz, "POST", "/api/v1/hooks/"+h.ID+"/test", TestRequest{
		Method: "PUT", Path: "/ping?x=1", Headers: http.Header{"X-A": {"1"}}, Body: "{}",
	}, &c)
	if resp.StatusCode != 201 || c.Kind != inspect.KindTest || c.Method != "PUT" || c.Path != "/ping" || c.Query != "x=1" ||
		c.Outcome != inspect.OutcomeOK || string(c.Attempts[0].ResponseBody.Body) != "pong" || c.Sender.Login != "moritz@example.com" {
		t.Fatalf("test request = %d %+v", resp.StatusCode, c)
	}
	// Defaults: POST to "/".
	c = inspect.Call{}
	if resp := e.call(moritz, "POST", "/api/v1/hooks/"+h.ID+"/test", TestRequest{}, &c); resp.StatusCode != 201 || c.Method != "POST" || c.Path != "/" {
		t.Fatalf("default test request = %d %+v", resp.StatusCode, c)
	}

	for field, req := range map[string]TestRequest{
		"method":         {Method: "GE T"},
		"path":           {Path: "nope"},
		"path ":          {Path: "//evil.example/x"}, // a host, not a path
		"headers.Bad Na": {Headers: http.Header{"Bad Name": {"x"}}},
	} {
		var p problem.Problem
		if resp := e.call(moritz, "POST", "/api/v1/hooks/"+h.ID+"/test", req, &p); resp.StatusCode != 422 || !strings.Contains(p.Detail, strings.TrimSpace(field)) {
			t.Errorf("%s: %d %+v, want 422 naming the field", field, resp.StatusCode, p)
		}
	}
	if resp := e.call(admin, "POST", "/api/v1/hooks/"+h.ID+"/test", TestRequest{}, nil); resp.StatusCode != 403 {
		t.Errorf("admin test request = %d, want 403", resp.StatusCode)
	}
}
