package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// API is a client for the relay's /api/v1. Errors from the relay are
// returned as *problem.Problem.
type API struct {
	BaseURL string
	HTTP    *http.Client
}

func (c *API) Me(ctx context.Context) (api.Me, error) {
	var me api.Me
	err := c.do(ctx, http.MethodGet, "/api/v1/me", nil, &me)
	return me, err
}

func (c *API) ListHooks(ctx context.Context) ([]api.Hook, error) {
	var hs []api.Hook
	err := c.do(ctx, http.MethodGet, "/api/v1/hooks", nil, &hs)
	return hs, err
}

func (c *API) CreateHook(ctx context.Context, req hooks.CreateRequest) (api.Hook, error) {
	var h api.Hook
	err := c.do(ctx, http.MethodPost, "/api/v1/hooks", req, &h)
	return h, err
}

func (c *API) DeleteHook(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/hooks/"+url.PathEscape(id), nil, nil)
}

// TunnelURL is the endpoint tunnel.Dial attaches to for the given hooks.
func (c *API) TunnelURL(hookIDs ...string) string {
	q := url.Values{"hook": hookIDs}
	return c.BaseURL + "/api/v1/tunnel?" + q.Encode()
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
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
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
