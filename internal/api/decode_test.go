package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

func TestQueryBool(t *testing.T) {
	type result struct{ value, ok bool }
	for q, want := range map[string]result{
		"dryRun=1":          {true, true},
		"dryRun=true":       {true, true},
		"dryRun=TRUE":       {true, true},
		"dryRun=false":      {false, true},
		"":                  {false, true},
		"dryRun=yes":        {false, false},
		"dryRun=":           {false, false},
		"dryRun=1&dryRun=1": {false, false},
		"dryrun=true":       {false, true}, // another parameter
	} {
		w := httptest.NewRecorder()
		value, ok := queryBool(w, httptest.NewRequest("POST", "/hooks?"+q, nil), "dryRun")
		if (result{value, ok}) != want || !ok && w.Code != 400 {
			t.Errorf("queryBool(%q) = %v, %v (%d), want %v", q, value, ok, w.Code, want)
		}
	}
}

func TestDecodeRequestNamesUnknownFields(t *testing.T) {
	for body, want := range map[string]string{
		`{"bogus":1}`:                    `[{"field":"bogus","message":"not supported by this relay"}]`,
		`{"settings":{"bogus":1,"x":2}}`: `[{"field":"settings.bogus","message":"not supported by this relay"},{"field":"settings.x","message":"not supported by this relay"}]`,
		`{"settings":{"rateLimit":{"perSecond":1,"y":1}},"z":null}`: `[{"field":"settings.rateLimit.y","message":"not supported by this relay"},{"field":"z","message":"not supported by this relay"}]`,
		// Names match exactly: encoding/json would apply these.
		`{"PATH":"x"}`:                      `[{"field":"PATH","message":"not supported by this relay"}]`,
		`{"settings":{"HoldTimeout":"1s"}}`: `[{"field":"settings.HoldTimeout","message":"not supported by this relay"}]`,
	} {
		w := httptest.NewRecorder()
		var req hooks.UpdateRequest
		if decodeRequest(w, httptest.NewRequest("PATCH", "/hooks/x", strings.NewReader(body)), &req, 1<<10) {
			t.Fatalf("%s decoded", body)
		}
		var p problem.Problem
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || w.Code != 422 || p.Code != problem.UnsupportedField {
			t.Fatalf("%s = %d %s", body, w.Code, w.Body)
		}
		if got := toJSON(p.Errors); got != want {
			t.Errorf("%s errors = %s, want %s", body, got, want)
		}
	}
	for _, body := range []string{`{"path":1}`, `{"path":"a"} {}`, `{"path":"a"} x`, ``} {
		w := httptest.NewRecorder()
		var req hooks.UpdateRequest
		if decodeRequest(w, httptest.NewRequest("PATCH", "/hooks/x", strings.NewReader(body)), &req, 1<<10) || w.Code != 400 {
			t.Errorf("%q = %d %s", body, w.Code, w.Body)
		}
	}
}
