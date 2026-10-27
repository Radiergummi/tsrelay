package client

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// NoHookError means the caller has no hook with the ID or path Ref.
type NoHookError struct{ Ref string }

func (e *NoHookError) Error() string { return fmt.Sprintf("no hook %q", e.Ref) }

// HookMismatchError means the caller's hook at the wanted path is not what
// EnsureHook was asked for, and EnsureHook won't change it on its own.
type HookMismatchError struct {
	Hook  api.Hook
	Field string // "channel" or "ephemeral"
	Want  string // for "channel": the channel asked for
}

func (e *HookMismatchError) Error() string {
	if e.Field == "channel" {
		return fmt.Sprintf("hook %q is in channel %q, not %q", e.Hook.Path, e.Hook.Channel, e.Want)
	}
	return fmt.Sprintf("hook %q is ephemeral", e.Hook.Path)
}

// FindHook is the hook of all that ref names by ID or path.
func FindHook(all []api.Hook, ref string) (api.Hook, error) {
	i := slices.IndexFunc(all, func(h api.Hook) bool { return h.ID == ref })
	if i < 0 {
		i = slices.IndexFunc(all, func(h api.Hook) bool { return h.Path == strings.Trim(ref, "/") })
	}
	if i < 0 {
		return api.Hook{}, &NoHookError{Ref: ref}
	}
	return all[i], nil
}

// ResolveHooks finds the caller's hooks named by ID or path, in order. It
// fails if any of them does not exist.
func (c *API) ResolveHooks(ctx context.Context, refs []string) ([]api.Hook, error) {
	all, err := c.ListHooks(ctx)
	if err != nil {
		return nil, err
	}
	found := make([]api.Hook, 0, len(refs))
	for _, ref := range refs {
		h, err := FindHook(all, ref)
		if err != nil {
			return nil, err
		}
		found = append(found, h)
	}
	return found, nil
}

// EnsureHook reuses the caller's hook at the wanted path or creates one. An
// explicit name wins over the remembered path. A remembered path that can no
// longer be created (e.g. another hook now overlaps it) falls back to a new
// random path; an explicit name never does. created reports whether this
// call made a new hook rather than reusing one that already existed.
func (c *API) EnsureHook(ctx context.Context, explicit, remembered string, req hooks.CreateRequest) (h api.Hook, created bool, err error) {
	want := cmp.Or(explicit, remembered)
	if want != "" {
		existing, err := c.ListHooks(ctx)
		if err != nil {
			return api.Hook{}, false, err
		}
		for _, h := range existing {
			if h.Path != want {
				continue
			}
			switch {
			case req.Channel != "" && h.Channel != req.Channel:
				return api.Hook{}, false, &HookMismatchError{Hook: h, Field: "channel", Want: req.Channel}
			case !req.Ephemeral && h.Ephemeral:
				return api.Hook{}, false, &HookMismatchError{Hook: h, Field: "ephemeral"}
			}
			return h, false, nil
		}
	}
	req.Path = want
	h, err = c.CreateHook(ctx, req)
	var p *problem.Problem
	if err != nil && explicit == "" && remembered != "" && errors.As(err, &p) && p.Status == http.StatusUnprocessableEntity {
		req.Path = ""
		h, err = c.CreateHook(ctx, req)
	}
	return h, err == nil, err
}
