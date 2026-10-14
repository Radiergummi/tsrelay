// Package server composes the relay: callback handler, API, guard, reaper.
package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/events"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/relay"
	"github.com/Radiergummi/tsrelay/internal/telemetry"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/Radiergummi/tsrelay/internal/ui"
	"github.com/Radiergummi/tsrelay/web"
	"tailscale.com/tailcfg"
)

const DefaultEphemeralGrace = 10 * time.Minute

type Config struct {
	BaseURL        string   // public URL of the relay on the tailnet, no trailing slash
	AllowedHosts   []string // Host header values the relay answers to
	CapName        string   // the capability carrying relay grants; GET /me names it
	EphemeralGrace time.Duration
	HoldQueue      int // calls per hook that may wait for a tunnel; 0 means relay.DefaultHoldQueue
	Tunnel         tunnel.Options
	Inspect        inspect.Config
	Telemetry      *telemetry.Telemetry // nil: Prometheus metrics only, no tracing

	NoTracePropagation bool // don't join senders' traces or pass the relay's to apps (--trace-propagation=false)
}

type Server struct {
	cfg     Config
	hooks   *hooks.Manager
	tunnels *tunnel.Registry
	audit   *audit.Logger
	relay   *relay.Handler // serves callbacks; its Held() feeds the held-calls gauge
	handler http.Handler
	inspect *inspect.Inspector
	events  *events.Hub

	// identifier re-identifies tunnel owners during revalidation.
	identifier identify.Identifier
	// identifyFailures counts each tunnel's consecutive failed identifications
	// during revalidation. Only the revalidator touches it.
	identifyFailures map[*tunnel.Tunnel]int
	tel              *telemetry.Telemetry
}

func New(cfg Config, id identify.Identifier, mgr *hooks.Manager, al *audit.Logger) *Server {
	if cfg.EphemeralGrace <= 0 {
		cfg.EphemeralGrace = DefaultEphemeralGrace
	}
	s := &Server{cfg: cfg, hooks: mgr, tunnels: tunnel.NewRegistry(), audit: al}
	s.identifier = id
	s.tel = cfg.Telemetry
	if s.tel == nil {
		s.tel = telemetry.Must(telemetry.Setup(context.Background(), telemetry.Config{}))
	}
	al.Observer = s.newMetrics(s.tel.Meter)
	s.inspect = inspect.New(cfg.Inspect)
	s.events = &events.Hub{}
	s.inspect.OnChange = func(owner tailcfg.UserID, sum inspect.Summary) {
		s.events.Publish(events.Event{Type: events.TypeCall, Owner: owner, HookID: string(sum.HookID), Data: sum})
	}
	s.relay = &relay.Handler{Identifier: id, Hooks: mgr, Tunnels: s.tunnels, Audit: al, HoldQueue: cfg.HoldQueue, Inspect: s.inspect}

	mux := http.NewServeMux()
	a := &api.API{Identifier: id, Hooks: mgr, Tunnels: s.tunnels, Inspect: s.inspect, Relay: s.relay, Events: s.events, Audit: al, BaseURL: cfg.BaseURL, CapName: cfg.CapName, Tunnel: cfg.Tunnel, Meter: s.tel.Meter.Meter(telemetry.ScopeName)}
	if s.tel.Tracing {
		s.relay.Tracer = s.tel.Tracer.Tracer(telemetry.ScopeName)
		if !cfg.NoTracePropagation {
			s.relay.Propagator = s.tel.Propagator
		}
		a.Tracer = s.tel.Tracer.Tracer(telemetry.ScopeName)
	}
	a.Register(mux)
	ui.New(a, web.Dist).Register(mux)

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
	runEvery(ctx, interval, func() {
		reaped, err := s.hooks.Reap(ctx, s.cfg.EphemeralGrace, func(id hooks.ID) bool {
			_, ok := s.tunnels.Lookup(string(id))
			return ok
		})
		if err != nil {
			s.audit.Slog().Error("reaping ephemeral hooks failed", "error", err)
		}
		for _, h := range reaped {
			s.audit.Log(ctx, audit.HookExpire, audit.Hook(h))
			s.tunnels.RemoveHook(string(h.ID))
			s.inspect.Forget(h.ID)
			s.events.Publish(events.HookChanged(h.Owner, string(h.ID)))
		}
	})
}

// runEvery calls f every interval until ctx is done.
func runEvery(ctx context.Context, interval time.Duration, f func()) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		f()
	}
}

// Close ends the event streams and closes all tunnels. Neither hijacked
// tunnel connections nor open streams are covered by http.Server.Shutdown.
func (s *Server) Close() {
	s.events.Close()
	s.tunnels.CloseAll()
}
