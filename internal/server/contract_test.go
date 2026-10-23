package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"go.yaml.in/yaml/v3"
)

// TestResponsesMatchOpenAPI sends real requests through the whole relay and
// checks each answer against its schema in openapi.yaml: every required
// field is there, every field is declared, and each has its declared type.
// Generated and validating clients rely on exactly that.
func TestResponsesMatchOpenAPI(t *testing.T) {
	raw, err := os.ReadFile("../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	s := schemas{doc: spec}

	e := newEnv(t, Config{})
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer local.Close()
	exp := "2030-01-01T00:00:00Z"
	h, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "contract", Description: "contract test",
		Settings: hooks.Settings{Capture: new(true)}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.cli(h.ID, local.URL)()
	e.send(h, "/x?q=1", []byte(`{"a":1}`), http.Header{"Content-Type": {"application/json"}})
	var calls struct {
		Calls []struct{ ID, Outcome string } `json:"calls"`
	}
	// The sender has the answer just before the relay records the call as
	// done, and a call still pending can't be replayed.
	e.waitFor(func() bool {
		e.getJSON("/hooks/"+h.ID+"/calls", &calls)
		return len(calls.Calls) > 0 && calls.Calls[0].Outcome != inspect.OutcomePending
	})
	callID := calls.Calls[0].ID

	for _, tc := range []struct {
		method, path, specPath, body string
		status                       int
	}{
		{"GET", "/", "/", "", 200},
		{"GET", "/hooks", "/hooks", "", 200},
		{"POST", "/hooks", "/hooks", `{"path":"made","expiresAt":"` + exp + `"}`, 201},
		{"POST", "/hooks?dryRun=true", "/hooks", `{"path":"dry"}`, 200},
		{"GET", "/hooks/" + h.ID, "/hooks/{id}", "", 200},
		{"PATCH", "/hooks/" + h.ID, "/hooks/{id}", `{"description":"changed"}`, 200},
		{"GET", "/hooks/" + h.ID + "/calls", "/hooks/{id}/calls", "", 200},
		{"GET", "/hooks/" + h.ID + "/calls/" + callID, "/hooks/{id}/calls/{callID}", "", 200},
		{"POST", "/hooks/" + h.ID + "/calls/" + callID + "/replay", "/hooks/{id}/calls/{callID}/replay", "", 201},
		{"POST", "/hooks/" + h.ID + "/test", "/hooks/{id}/test", `{"path":"/t","body":"{}"}`, 201},
		{"GET", "/channels", "/channels", "", 200},
		{"GET", "/channels/default", "/channels/{name}", "", 200},
		{"GET", "/licenses", "/licenses", "", 200},
		{"GET", "/hooks/h_missing", "/hooks/{id}", "", 404},
		{"POST", "/hooks", "/hooks", `{"channel":"Not Valid"}`, 422},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req, _ := http.NewRequest(tc.method, e.base+tc.path, body)
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Content-Type", "application/json") // the API wants it on every write, body or not
			resp, err := e.api.HTTP.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode == http.StatusNotFound && tc.path == "/licenses" {
				t.Skip("no SBOM in test builds")
			}
			if resp.StatusCode != tc.status {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode, tc.status, b)
			}
			schema, ok := s.response(tc.specPath, strings.ToLower(tc.method), resp.StatusCode)
			if !ok {
				t.Fatalf("openapi.yaml declares no JSON response %d for %s %s", resp.StatusCode, tc.method, tc.specPath)
			}
			var v any
			dec := json.NewDecoder(bytes.NewReader(b))
			dec.UseNumber()
			if err := dec.Decode(&v); err != nil {
				t.Fatalf("not JSON: %v: %s", err, b)
			}
			for _, problem := range s.check(schema, v, "$") {
				t.Error(problem)
			}
		})
	}
}

// schemas validates JSON values against the subset of JSON Schema that
// openapi.yaml uses.
type schemas struct{ doc map[string]any }

// response is the JSON schema of the answer to method at path with status,
// or the default answer's.
func (s schemas) response(path, method string, status int) (any, bool) {
	op, _ := dig(s.doc, "paths", path, method).(map[string]any)
	responses, _ := op["responses"].(map[string]any)
	r, ok := responses[strconv.Itoa(status)]
	if !ok {
		r, ok = responses["default"]
	}
	if !ok {
		return nil, false
	}
	r = s.resolve(r)
	schema := dig(r, "content", "application/json", "schema")
	if schema == nil {
		schema = dig(r, "content", "application/problem+json", "schema")
	}
	return schema, schema != nil
}

func dig(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// resolve follows $ref to the definition in the document.
func (s schemas) resolve(v any) any {
	for {
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		ref, ok := m["$ref"].(string)
		if !ok {
			return v
		}
		v = dig(s.doc, strings.Split(strings.TrimPrefix(ref, "#/"), "/")...)
	}
}

// check returns what is wrong with v under schema, at path.
func (s schemas) check(schema, v any, path string) []string {
	sc, _ := s.resolve(schema).(map[string]any)
	if sc == nil {
		return nil
	}
	if alts, ok := sc["oneOf"].([]any); ok {
		return s.checkAny(alts, v, path)
	}
	if alts, ok := sc["anyOf"].([]any); ok {
		return s.checkAny(alts, v, path)
	}
	if parts, ok := sc["allOf"].([]any); ok {
		sc = s.merge(parts)
	}
	var problems []string
	if types := typesOf(sc); len(types) > 0 && !slices.Contains(types, jsonType(v)) &&
		!(jsonType(v) == "integer" && slices.Contains(types, "number")) {
		return []string{fmt.Sprintf("%s: %s, want %s", path, jsonType(v), strings.Join(types, " or "))}
	}
	if enum, ok := sc["enum"].([]any); ok && !slices.ContainsFunc(enum, func(e any) bool { return fmt.Sprint(e) == fmt.Sprint(v) || e == nil && v == nil }) {
		problems = append(problems, fmt.Sprintf("%s: %v is not one of %v", path, v, enum))
	}
	if ts, ok := v.(string); ok && sc["format"] == "date-time" && !strings.HasSuffix(ts, "Z") {
		problems = append(problems, fmt.Sprintf("%s: %s is not in UTC", path, ts))
	}
	switch v := v.(type) {
	case map[string]any:
		props, _ := sc["properties"].(map[string]any)
		for _, r := range asList(sc["required"]) {
			if _, ok := v[r]; !ok {
				problems = append(problems, fmt.Sprintf("%s: required field %q is missing", path, r))
			}
		}
		extra, hasExtra := sc["additionalProperties"]
		for k, fv := range v {
			switch fs, declared := props[k]; {
			case declared:
				problems = append(problems, s.check(fs, fv, path+"."+k)...)
			case hasExtra && extra != false:
				problems = append(problems, s.check(extra, fv, path+"."+k)...)
			case props != nil || hasExtra:
				problems = append(problems, fmt.Sprintf("%s: field %q is not declared", path, k))
			}
		}
	case []any:
		for i, e := range v {
			problems = append(problems, s.check(sc["items"], e, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return problems
}

func (s schemas) checkAny(alts []any, v any, path string) []string {
	var all []string
	for _, a := range alts {
		problems := s.check(a, v, path)
		if len(problems) == 0 {
			return nil
		}
		all = append(all, problems...)
	}
	return []string{fmt.Sprintf("%s: matches none of the alternatives: %s", path, strings.Join(all, "; "))}
}

// merge combines the parts of an allOf into one object schema.
func (s schemas) merge(parts []any) map[string]any {
	out := map[string]any{"type": "object", "properties": map[string]any{}}
	var required []any
	for _, p := range parts {
		pm, _ := s.resolve(p).(map[string]any)
		if inner, ok := pm["allOf"].([]any); ok {
			pm = s.merge(inner)
		}
		props, _ := pm["properties"].(map[string]any)
		for k, v := range props {
			out["properties"].(map[string]any)[k] = v
		}
		required = append(required, pm["required"].([]any)...)
	}
	out["required"] = required
	return out
}

func typesOf(sc map[string]any) []string {
	switch t := sc["type"].(type) {
	case string:
		return []string{t}
	case []any:
		return asList(t)
	}
	return nil
}

func asList(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, e := range l {
		out = append(out, fmt.Sprint(e))
	}
	return out
}

func jsonType(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number:
		if _, err := v.Int64(); err == nil {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}
