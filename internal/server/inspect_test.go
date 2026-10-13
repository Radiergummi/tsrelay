package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/inspect"
)

// getJSON GETs path as the receiver and decodes a 2xx answer into out.
func (e *env) getJSON(path string, out any) int {
	e.t.Helper()
	resp, err := e.api.HTTP.Get(e.base + path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 && out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

// postJSON POSTs in (nil for no body) as the receiver and decodes a 2xx
// answer into out.
func (e *env) postJSON(path string, in, out any) int {
	e.t.Helper()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest("POST", e.base+path, body)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.api.HTTP.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 && out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestCallsEndToEnd(t *testing.T) {
	e := newEnv(t, Config{})
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	defer local.Close()
	h, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "cb"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.cli(h.ID, local.URL)()
	resp := e.send(h, "/done", []byte("{}"), nil)
	io.ReadAll(resp.Body)
	var list []inspect.Summary
	e.waitFor(func() bool {
		list = nil
		return e.getJSON("/api/v1/hooks/"+h.ID+"/calls", &list) == 200 && len(list) == 1 && list[0].Outcome == inspect.OutcomeOK
	})
	if list[0].ID != resp.Header.Get("Tsrelay-Call-Id") || list[0].Status != 202 || list[0].Path != "/done" {
		t.Fatalf("summary = %+v", list[0])
	}
}
