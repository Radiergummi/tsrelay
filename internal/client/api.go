package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// UnreachableError means the relay's base URL could not even be reached: a
// network failure from the HTTP client itself, not a response the relay
// sent (that would be a *problem.Problem instead).
type UnreachableError struct {
	Base string
	Err  error
}

func (e *UnreachableError) Error() string {
	return fmt.Sprintf("could not reach the relay at %s: %s", e.Base, e.Err)
}

func (e *UnreachableError) Unwrap() error { return e.Err }

// API is a client for the relay's resources, which it asks for as JSON.
// Errors from the relay are returned as *problem.Problem.
type API struct {
	BaseURL string
	HTTP    *http.Client
}

func (c *API) Me(ctx context.Context) (api.Me, error) {
	var me api.Me
	err := c.do(ctx, http.MethodGet, "/", nil, &me)
	return me, err
}

func (c *API) ListHooks(ctx context.Context) ([]api.Hook, error) {
	var hs []api.Hook
	err := c.do(ctx, http.MethodGet, "/hooks", nil, &hs)
	return hs, err
}

func (c *API) CreateHook(ctx context.Context, req hooks.CreateRequest) (api.Hook, error) {
	var h api.Hook
	err := c.do(ctx, http.MethodPost, "/hooks", req, &h)
	return h, err
}

func (c *API) UpdateHook(ctx context.Context, id string, req hooks.UpdateRequest) (api.Hook, error) {
	var h api.Hook
	err := c.do(ctx, http.MethodPatch, "/hooks/"+url.PathEscape(id), req, &h)
	return h, err
}

func (c *API) DeleteHook(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/hooks/"+url.PathEscape(id), nil, nil)
}

func (c *API) RotatePublicURL(ctx context.Context, id string) (api.Hook, error) {
	var h api.Hook
	err := c.do(ctx, http.MethodPost, "/hooks/"+url.PathEscape(id)+"/rotate-public-url", nil, &h)
	return h, err
}

// TunnelURL is the endpoint tunnel.Dial attaches to for the given hooks.
func (c *API) TunnelURL(a tunnel.Attach, hookIDs ...string) string {
	q := url.Values{"hook": hookIDs}
	if a.Session != "" {
		q.Set("session", a.Session)
	}
	if a.Resume {
		q.Set("resume", "1")
	}
	if a.Target != "" {
		q.Set("target", a.Target)
	}
	return c.BaseURL + "/tunnel?" + q.Encode()
}

func (c *API) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return &UnreachableError{Base: c.BaseURL, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return problem.Decode(resp)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
