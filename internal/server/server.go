// Package server composes the relay: callback handler, API, guard, reaper.
package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/metrics"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/relay"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

const DefaultEphemeralGrace = 10 * time.Minute

type Config struct {
	BaseURL        string   // public URL of the relay on the tailnet, no trailing slash
	AllowedHosts   []string // Host header values the relay answers to
	EphemeralGrace time.Duration
	HoldQueue      int // calls per hook that may wait for a tunnel; 0 means relay.DefaultHoldQueue
	Tunnel         tunnel.Options
}

type Server struct {
	cfg     Config
	hooks   *hooks.Manager
	tunnels *tunnel.Registry
	audit   *audit.Logger
	relay   *relay.Handler // serves callbacks; its Held() feeds the held-calls gauge
	handler http.Handler

	// identifier re-identifies tunnel owners during revalidation.
	identifier identify.Identifier
	// identifyFailures counts each tunnel's consecutive failed identifications
	// during revalidation. Only the revalidator touches it.
	identifyFailures map[*tunnel.Tunnel]int
	metrics          *metrics.Registry
}

func New(cfg Config, id identify.Identifier, mgr *hooks.Manager, al *audit.Logger) *Server {
	if cfg.EphemeralGrace <= 0 {
		cfg.EphemeralGrace = DefaultEphemeralGrace
	}
	s := &Server{cfg: cfg, hooks: mgr, tunnels: tunnel.NewRegistry(), audit: al}
	s.identifier = id
	s.metrics, al.Observer = s.newMetrics()
	s.relay = &relay.Handler{Identifier: id, Hooks: mgr, Tunnels: s.tunnels, Audit: al, HoldQueue: cfg.HoldQueue}

	mux := http.NewServeMux()
	(&api.API{Identifier: id, Hooks: mgr, Tunnels: s.tunnels, Audit: al, BaseURL: cfg.BaseURL, Tunnel: cfg.Tunnel}).Register(mux)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		// Placeholder until the web UI lands (plan 3).
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "tsrelay is running. The web UI is not available yet; the API is at /api/v1/.\n")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		problem.New(http.StatusNotFound, problem.NotFound, "not found").Write(w)
	})

	// Callbacks bypass the mux, whose path cleaning would rewrite paths
	// such as "/~m/cb//x" that must reach the local app unchanged.
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/~") {
			s.relay.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	s.handler = api.Guard(cfg.AllowedHosts, root)
	return s
}

func (s *Server) Handler() http.Handler { return s.handler }

// RunReaper deletes expired ephemeral hooks every interval until ctx is done.
func (s *Server) RunReaper(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		reaped, err := s.hooks.Reap(ctx, s.cfg.EphemeralGrace, func(id hooks.ID) bool {
			_, ok := s.tunnels.Lookup(string(id))
			return ok
		})
		if err != nil {
			s.audit.Slog().Error("reaping ephemeral hooks failed", "error", err)
		}
		for _, h := range reaped {
			s.audit.Log(ctx, audit.HookExpire, audit.Hook(h))
			s.tunnels.Release(string(h.ID)) // calls held for the hook fail now
			if t, orphaned, ok := s.tunnels.DetachHook(string(h.ID)); ok {
				t.Notify(tunnel.Control{Type: tunnel.ControlHookDeleted, Hooks: []tunnel.HookInfo{{ID: string(h.ID)}}}, orphaned)
			}
		}
	}
}

// Close closes all tunnels. Hijacked tunnel connections are not covered by
// http.Server.Shutdown.
func (s *Server) Close() { s.tunnels.CloseAll() }
