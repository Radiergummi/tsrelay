package client

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// fakeHooks serves GET /hooks with existing, and POST /hooks by refusing
// the path "taken" with 422 and creating any other, "random" when empty.
func fakeHooks(t *testing.T, existing ...api.Hook) *API {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(api.HookList{Hooks: existing})
			return
		}
		var req hooks.CreateRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Path == "taken" {
			problem.InvalidFields(problem.Invalid, problem.FieldErrors{{Field: "path", Message: "conflicts"}}).Write(w)
			return
		}
		json.NewEncoder(w).Encode(api.Hook{ID: "h_new", Path: cmp.Or(req.Path, "random"), Channel: req.Channel})
	}))
	t.Cleanup(srv.Close)
	return &API{BaseURL: srv.URL, HTTP: srv.Client()}
}

func TestEnsureHook(t *testing.T) {
	ctx := context.Background()
	c := fakeHooks(t, api.Hook{ID: "h_1", Path: "cb", Channel: "default"}, api.Hook{ID: "h_2", Path: "eph", Channel: "default", Ephemeral: true})

	if h, created, err := c.EnsureHook(ctx, "cb", "", hooks.CreateRequest{}); err != nil || created || h.ID != "h_1" {
		t.Errorf("existing path: %+v, created %v, %v; want h_1 reused", h, created, err)
	}
	if h, created, err := c.EnsureHook(ctx, "", "taken", hooks.CreateRequest{}); err != nil || !created || h.Path != "random" {
		t.Errorf("remembered path refused: %+v, created %v, %v; want a new random path", h, created, err)
	}
	var p *problem.Problem
	if _, _, err := c.EnsureHook(ctx, "taken", "", hooks.CreateRequest{}); !errors.As(err, &p) || p.Status != http.StatusUnprocessableEntity {
		t.Errorf("explicit path refused: %v, want the 422, not a random path", err)
	}
	var hm *HookMismatchError
	if _, _, err := c.EnsureHook(ctx, "cb", "", hooks.CreateRequest{Channel: "prod"}); !errors.As(err, &hm) || hm.Field != "channel" || hm.Want != "prod" {
		t.Errorf("other channel: %v, want a channel mismatch", err)
	}
	if _, _, err := c.EnsureHook(ctx, "eph", "", hooks.CreateRequest{}); !errors.As(err, &hm) || hm.Field != "ephemeral" {
		t.Errorf("persistent wanted: %v, want an ephemeral mismatch", err)
	}
}

func TestResolveHooks(t *testing.T) {
	c := fakeHooks(t, api.Hook{ID: "h_1", Path: "cb"}, api.Hook{ID: "h_2", Path: "a/b"})
	found, err := c.ResolveHooks(context.Background(), []string{"/a/b/", "h_1"})
	if err != nil || len(found) != 2 || found[0].ID != "h_2" || found[1].ID != "h_1" {
		t.Fatalf("ResolveHooks = %+v, %v", found, err)
	}
	var nh *NoHookError
	if _, err := c.ResolveHooks(context.Background(), []string{"h_1", "nope"}); !errors.As(err, &nh) || nh.Ref != "nope" {
		t.Fatalf("missing ref: %v, want NoHookError for nope", err)
	}
}
