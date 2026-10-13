package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/inspect"
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
