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
	for q, want := range map[string]bool{
		"dryRun=1":    true,
		"dryRun=true": true,
		"dryRun=TRUE": true,
		"dryRun=yes":  false,
		"dryRun=":     false,
		"":            false,
	} {
		if got := queryBool(httptest.NewRequest("POST", "/hooks?"+q, nil), "dryRun"); got != want {
			t.Errorf("queryBool(%q) = %v, want %v", q, got, want)
		}
	}
}

func TestDecodeRequestNamesUnknownFields(t *testing.T) {
	for body, want := range map[string]string{
		`{"bogus":1}`:                    `[{"field":"bogus","message":"not supported by this relay"}]`,
		`{"settings":{"bogus":1,"x":2}}`: `[{"field":"settings.bogus","message":"not supported by this relay"},{"field":"settings.x","message":"not supported by this relay"}]`,
		`{"Settings":{"rateLimit":{"perSecond":1,"y":1}},"z":null}`: `[{"field":"settings.rateLimit.y","message":"not supported by this relay"},{"field":"z","message":"not supported by this relay"}]`,
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
	w := httptest.NewRecorder()
	var req hooks.UpdateRequest
	if decodeRequest(w, httptest.NewRequest("PATCH", "/hooks/x", strings.NewReader(`{"path":1}`)), &req, 1<<10) || w.Code != 400 {
		t.Fatalf("wrong type = %d %s", w.Code, w.Body)
	}
}
