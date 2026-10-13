package relay

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/inspect"
)

// ErrNotRouted means an injected call did not reach its hook: the hook was
// deleted, or its path moved, before the call was routed.
var ErrNotRouted = errors.New("relay: the call did not reach the hook")

type injectKey struct{}

// injection marks a request built by Inject. ServeHTTP delivers it without
// identifying or authorizing a sender, and fills in callID.
type injection struct {
	hook     hooks.ID
	kind     inspect.Kind
	replayOf string
	sender   authz.Identity
	callID   string
}

// Injection describes a replay or a test request (DESIGN.md §9.3).
type Injection struct {
	Hook     hooks.Hook
	Kind     inspect.Kind   // inspect.KindReplay or inspect.KindTest
	ReplayOf string         // for a replay: the replayed call's ID
	Sender   authz.Identity // whom the Tsrelay-Sender-* headers name
	Method   string
	Path     string // escaped, below the hook's route; "" means "/"
	Query    string
	Header   http.Header
	Body     []byte
	// From is the API request that asked for the call. Its remote address,
	// host and TLS state stand in for a sender's connection.
	From *http.Request
}

// Inject delivers a replay or test call through the normal pipeline, minus
// sender identification and authorization, and returns the finished call.
// The response goes to the inspector only. The handler needs an inspector.
func (h *Handler) Inject(ctx context.Context, in Injection) (inspect.Call, error) {
	if h.Inspect == nil {
		return inspect.Call{}, errors.New("relay: Inject needs an inspector")
	}
	path := in.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return inspect.Call{}, errors.New("relay: an injected path must start with /")
	}
	u, err := url.Parse(in.Hook.Route() + path)
	if err != nil {
		return inspect.Call{}, err
	}
	u.RawQuery = in.Query
	inj := &injection{hook: in.Hook.ID, kind: in.Kind, replayOf: in.ReplayOf, sender: in.Sender}
	req, err := http.NewRequestWithContext(context.WithValue(ctx, injectKey{}, inj), in.Method, u.String(), bytes.NewReader(in.Body))
	if err != nil {
		return inspect.Call{}, err
	}
	req.Header = in.Header.Clone()
	if req.Header == nil {
		req.Header = http.Header{}
	}
	req.Host, req.RemoteAddr, req.TLS = in.From.Host, in.From.RemoteAddr, in.From.TLS
	serveInjected(h, &discard{header: http.Header{}}, req)
	c, ok := h.Inspect.Get(inj.callID)
	if !ok {
		return inspect.Call{}, ErrNotRouted
	}
	return c, nil
}

// serveInjected runs ServeHTTP and swallows exactly http.ErrAbortHandler: a
// tunnel that fails mid-response makes the reverse proxy panic with it
// (ctx carries http.ServerContextKey, as a real call's does), but the
// outcome is already recorded by then, so Inject must return the call
// instead of the panic escaping into the API's own request handling.
func serveInjected(h *Handler, w http.ResponseWriter, r *http.Request) {
	defer func() {
		if p := recover(); p != nil && p != http.ErrAbortHandler {
			panic(p)
		}
	}()
	h.ServeHTTP(w, r)
}

// discard is the ResponseWriter of an injected call: the inspector records
// the response, and it goes nowhere else.
type discard struct{ header http.Header }

func (d *discard) Header() http.Header         { return d.header }
func (d *discard) Write(b []byte) (int, error) { return len(b), nil }
func (d *discard) WriteHeader(int)             {}
func (d *discard) Flush()                      {}
