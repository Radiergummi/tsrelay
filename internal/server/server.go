// Package server composes the relay: callback handler, API, guard, reaper.
package server

import (
	"cmp"
	"context"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/events"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/lifecycle"
	"github.com/Radiergummi/tsrelay/internal/relay"
	"github.com/Radiergummi/tsrelay/internal/release"
	"github.com/Radiergummi/tsrelay/internal/telemetry"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/Radiergummi/tsrelay/internal/ui"
	"github.com/Radiergummi/tsrelay/web"
	"tailscale.com/tailcfg"
)

const DefaultEphemeralGrace = 10 * time.Minute

type Config struct {
	BaseURL        string   // public URL of the relay on the tailnet, no trailing slash
	PublicBaseURL  string   // the relay's URL on the internet, through Funnel; empty: no public hooks are served
	PublicMaxBody  int64    // body cap of public calls; 0: only each hook's own limit
	AllowedHosts   []string // Host header values the relay answers to
	CapName        string   // the capability carrying relay grants; GET / names it
	Version        string   // the relay's version, sent as api.VersionHeader; "" means "dev"
	Commit         string   // the revision the relay was built from; "" means the one Go stamped, if any
	EphemeralGrace time.Duration
	Revalidate     time.Duration // how often tunnels and event streams are authorized again; 0: never
	HoldQueue      int           // calls per hook that may wait for a tunnel; 0 means relay.DefaultHoldQueue
	Tunnel         tunnel.Options
	Inspect        inspect.Config
	Telemetry      *telemetry.Telemetry // nil: Prometheus metrics only, no tracing
	Releases       *release.Checker     // the latest release, named to admins in GET /; nil: none (--update-check=false)
	ReleaseAssets  string               // base URL of the release assets /download redirects to; "" means GitHub's

	NoTracePropagation bool // don't join senders' traces or pass the relay's to apps (--otel-propagation=false)
}

type Server struct {
	cfg     Config
	hooks   *hooks.Manager
	tunnels *lifecycle.Lifecycle
	audit   *audit.Logger
	relay   *relay.Handler // serves callbacks; its Held() feeds the held-calls gauge
	router  *api.Router
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
	s := &Server{cfg: cfg, hooks: mgr, audit: al}
	s.identifier = id
	s.tel = cfg.Telemetry
	if s.tel == nil {
		s.tel = telemetry.Must(telemetry.Setup(context.Background(), telemetry.Config{}))
	}
	onCall, onAudit := s.newMetrics(s.tel.Meter)
	al.Observer = onAudit
	s.inspect = inspect.New(cfg.Inspect)
	s.events = &events.Hub{}
	s.inspect.OnChange = func(owner tailcfg.UserID, channel string, sum inspect.Summary) {
		s.events.Publish(events.Event{Type: events.TypeCall, Owner: owner, Channel: channel, HookID: string(sum.HookID), Data: sum})
	}
	s.tunnels = lifecycle.New(lifecycle.Config{Hooks: mgr, Inspect: s.inspect, Events: s.events, Audit: al, BaseURL: cfg.BaseURL, PublicBaseURL: cfg.PublicBaseURL,
		Identify: func(ctx context.Context, remoteAddr string) (authz.Identity, error) {
			ctx, cancel := context.WithTimeout(ctx, identifyTimeout)
			defer cancel()
			return id.Identify(ctx, remoteAddr)
		}})
	s.relay = &relay.Handler{Identifier: id, Hooks: mgr, Tunnels: s.tunnels, Audit: al, HoldQueue: cfg.HoldQueue, Inspect: s.inspect, PublicMaxBody: cfg.PublicMaxBody, OnCall: onCall}

	version := cmp.Or(cfg.Version, "dev")
	a := &api.API{Identifier: id, Hooks: mgr, Tunnels: s.tunnels, Inspect: s.inspect, Relay: s.relay, Events: s.events, Audit: al, BaseURL: cfg.BaseURL, PublicBaseURL: cfg.PublicBaseURL, CapName: cfg.CapName, Version: version, Commit: cmp.Or(cfg.Commit, commit()), Releases: cfg.Releases, Tunnel: cfg.Tunnel, Meter: s.tel.Meter.Meter(telemetry.ScopeName), ReidentifyEvery: cfg.Revalidate}
	if s.tel.Tracing {
		s.relay.Tracer = s.tel.Tracer.Tracer(telemetry.ScopeName)
		if !cfg.NoTracePropagation {
			s.relay.Propagator = s.tel.Propagator
		}
		a.Tracer = s.tel.Tracer.Tracer(telemetry.ScopeName)
	}
	u := ui.New(a, web.Dist)
	a.HTML = u.Index()
	s.router = api.NewRouter(api.RouterConfig{
		API:           a,
		Inspect:       u.Inspect(),
		Fallback:      u.Fallback(),
		ReleaseAssets: cfg.ReleaseAssets,
		AllowedHosts:  cfg.AllowedHosts,
	})
	return s
}

func (s *Server) Handler() http.Handler { return s.router.Tailnet }

// Routes are every pattern the relay serves, for proving each one against
// the access rules.
func (s *Server) Routes() []string { return s.router.Routes }

// PublicHandler serves the hooks made public, for the Funnel listener. It
// is nil when Config.PublicBaseURL is empty.
func (s *Server) PublicHandler() http.Handler { return s.router.Public }

// commit is the revision this binary was built from, as the Go toolchain
// stamped it, or "" when it was built outside a checkout (as in Docker).
func commit() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
	}
	return ""
}

// RunReaper deletes expired ephemeral hooks every interval until ctx is done.
func (s *Server) RunReaper(ctx context.Context, interval time.Duration) {
	runEvery(ctx, interval, func() {
		if err := s.tunnels.Reap(ctx, s.cfg.EphemeralGrace); err != nil {
			s.audit.Slog().Error("reaping ephemeral hooks failed", "error", err)
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

// Drain prepares a shutdown: held calls are answered, as no tunnel can
// attach any more, and event streams end, which http.Server.Shutdown would
// otherwise wait for. Calls already delivered keep their tunnels until Close.
func (s *Server) Drain() {
	s.relay.Drain()
	s.events.Close()
}

// Close ends the event streams and closes all tunnels. Neither hijacked
// tunnel connections nor open streams are covered by http.Server.Shutdown.
func (s *Server) Close() {
	s.events.Close()
	s.tunnels.Close()
}
