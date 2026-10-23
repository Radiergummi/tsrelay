package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

func (e *env) postHooks(body string) (*http.Response, problem.Problem) {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.base+"/hooks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.api.HTTP.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var p problem.Problem
	_ = json.NewDecoder(resp.Body).Decode(&p)
	return resp, p
}

func TestUnknownFieldIsUnsupported(t *testing.T) {
	e := newEnv(t, Config{})
	for name, tc := range map[string]struct{ body, field string }{
		"top level": {`{"bogus":1}`, "bogus"},
		"nested":    {`{"settings":{"bogus":1}}`, "settings.bogus"},
	} {
		t.Run(name, func(t *testing.T) {
			resp, p := e.postHooks(tc.body)
			if resp.StatusCode != 422 || resp.Header.Get("Tsrelay-Error") != problem.UnsupportedField {
				t.Fatalf("status %d, Tsrelay-Error %q, want 422 %s", resp.StatusCode, resp.Header.Get("Tsrelay-Error"), problem.UnsupportedField)
			}
			errs, _ := json.Marshal(p.Errors)
			want, _ := json.Marshal([]hooks.FieldError{{Field: tc.field, Message: "not supported by this relay"}})
			if string(errs) != string(want) {
				t.Fatalf("errors = %s, want %s", errs, want)
			}
		})
	}
}

func TestMalformedJSONIsBadRequest(t *testing.T) {
	e := newEnv(t, Config{})
	resp, _ := e.postHooks(`{`)
	if resp.StatusCode != 400 || resp.Header.Get("Tsrelay-Error") != problem.BadRequest {
		t.Fatalf("status %d, Tsrelay-Error %q, want 400 bad_request", resp.StatusCode, resp.Header.Get("Tsrelay-Error"))
	}
}
