package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/inspect"
	"github.com/Radiergummi/tsrelay/internal/relay"
	"github.com/Radiergummi/tsrelay/internal/server"
	"github.com/Radiergummi/tsrelay/internal/telemetry"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/spf13/cobra"
	_ "tailscale.com/feature/oauthkey" // lets TS_CLIENT_SECRET mint auth keys
	"tailscale.com/tailcfg/peercap"
	"tailscale.com/tsnet"
)

func newServeCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the relay as its own tailnet node",
		Long: "Run the relay as its own tailnet node. Every flag can also be set as " +
			"TSRELAY_<FLAG>, e.g. TSRELAY_STATE_DIR for --state-dir.",
		Args: usageArgs(cobra.NoArgs),
	}
	fs := cmd.Flags()
	hostname := fs.String("hostname", "tsrelay", "tailnet machine name of the relay")
	stateDir := fs.String("state-dir", defaultStateDir(), "directory for tailnet state and hooks")
	useTLS := fs.Bool("tls", true, "serve HTTPS on 443 with a tailnet certificate; false serves plain HTTP on 80")
	tags := listFlag{Values: []string{"tag:tsrelay"}}
	fs.Var(&tags, "advertise-tags", "tags the relay node advertises (comma-separated)")
	capName := fs.String("cap-name", "mazetti.me/cap/relay", "application capability carrying relay grants")
	slugMode := fs.String("user-slug", string(hooks.SlugLocalPart), `how URL slugs derive from logins: "local-part" or "login"`)
	overrides := mapFlag{}
	fs.Var(overrides, "user-slug-override", "login=slug pairs that resolve slug collisions (repeatable)")
	maxHooks := fs.Int("max-hooks-default", authz.DefaultLimits.MaxHooks, "hooks per user unless a grant allows more")
	maxTunnels := fs.Int("max-tunnels-default", authz.DefaultLimits.MaxTunnels, "tunnels per user unless a grant allows more")
	maxTTL := fs.Duration("max-hook-ttl-default", 0, "longest hook lifetime unless a grant allows more (0: unlimited)")
	grace := fs.Duration("ephemeral-grace", server.DefaultEphemeralGrace, "how long an ephemeral hook survives without a tunnel")
	policyFlags := registerPolicyFlags(fs)
	concurrency := fs.Int("tunnel-concurrency", tunnel.DefaultConcurrency, "concurrent calls per tunnel")
	revalidate := fs.Duration("revalidate-interval", time.Minute, "how often attached tunnels are re-authorized")
	opsListen := fs.String("ops-listen", "", "host address for /healthz, /readyz and /metrics, e.g. :9090; never on the tailnet (empty: off)")
	opsPprof := fs.Bool("ops-pprof", false, "serve /debug/pprof/ on the ops listener")
	funnel := fs.Bool("funnel", false, "serve hooks their owners made public on the internet, through Tailscale Funnel")
	funnelPort := fs.Int("funnel-port", 443, "the Funnel port for public hooks: 443, 8443 or 10000")
	funnelMaxBody := fs.Int64("funnel-max-body-bytes", 25<<20, "largest request body of a call from the internet, in bytes")
	otlp := fs.Bool("otel", false, "export traces and metrics over OTLP/HTTP, configured by the standard OTEL_* environment variables")
	propagate := fs.Bool("trace-propagation", true, "with --otel: join senders' traces, and pass the relay's trace context on to apps")
	holdQueue := fs.Int("hold-queue", relay.DefaultHoldQueue, "calls per hook that may wait for a tunnel at once")
	captureLimit := fs.Int("capture-limit", inspect.DefaultCaptureLimit, "bytes the inspector keeps of each body, of each call's headers, and of its query string")
	captureCalls := fs.Int("capture-calls", inspect.DefaultCallsPerHook, "calls per hook the inspector keeps")
	captureBodies := fs.Bool("capture-bodies", true, "keep bodies for the inspector; false records metadata and headers only, whatever hooks set")
	redact := listFlag{Values: slices.Clone(inspect.DefaultRedact)}
	fs.Var(&redact, "redact-headers", "headers whose values the inspector never keeps (comma-separated)")
	logFormat := fs.String("log-format", "json", `the log line format: "json" or "text"`)
	logLevel := fs.String("log-level", "info", "debug, info, warn or error")
	verbose := fs.Bool("verbose-tailscale", false, "include tsnet's own logs")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		stderr := c.stderr
		if *slugMode != string(hooks.SlugLocalPart) && *slugMode != string(hooks.SlugLogin) {
			return usagef("--user-slug must be %q or %q", hooks.SlugLocalPart, hooks.SlugLogin)
		}
		if *revalidate <= 0 {
			return usagef("--revalidate-interval must be positive")
		}
		if *holdQueue < 1 {
			return usagef("--hold-queue must be at least 1; use --hook-hold-timeout-default=0s to disable holding")
		}
		if *captureLimit < 1 || *captureCalls < 1 {
			return usagef("--capture-limit and --capture-calls must be at least 1; use --capture-bodies=false to keep no bodies")
		}
		if *funnel && !*useTLS {
			return usagef("--funnel needs --tls: Funnel only serves HTTPS")
		}
		if *funnelPort != 443 && *funnelPort != 8443 && *funnelPort != 10000 {
			return usagef("--funnel-port must be 443, 8443 or 10000, the ports Funnel supports")
		}
		if *funnelMaxBody < 1 {
			return usagef("--funnel-max-body-bytes must be at least 1")
		}
		for login, slug := range overrides {
			if !hooks.ValidSlug(slug) {
				return usagef("--user-slug-override %s=%s: a slug may only contain a-z, 0-9, '.', '_' and '-'", login, slug)
			}
		}
		logger, err := newLogger(stderr, *logFormat, *logLevel)
		if err != nil {
			return &usageError{err.Error()}
		}
		policy, err := policyFlags()
		if err != nil {
			return &usageError{err.Error()}
		}

		err = runRelay(ctx, logger, relayConfig{
			hostname: *hostname, stateDir: *stateDir, tls: *useTLS, tags: tags.Values, verbose: *verbose,
			cap:    peercap.Cap(*capName),
			slugs:  hooks.SlugConfig{Mode: hooks.SlugMode(*slugMode), Overrides: overrides},
			limits: authz.Limits{MaxHooks: *maxHooks, MaxTunnels: *maxTunnels, MaxHookTTL: *maxTTL},
			policy: policy,
			server: server.Config{
				EphemeralGrace: *grace, HoldQueue: *holdQueue, Tunnel: tunnel.Options{Concurrency: *concurrency},
				Inspect: inspect.Config{CallsPerHook: *captureCalls, CaptureLimit: *captureLimit, NoBodies: !*captureBodies, Redact: redact.Values},
				CapName: *capName, NoTracePropagation: !*propagate,
			},
			revalidate: *revalidate,
			opsListen:  *opsListen,
			otel:       *otlp, pprof: *opsPprof,
			funnel: *funnel, funnelPort: *funnelPort, funnelMaxBody: *funnelMaxBody,
		})
		if err != nil {
			logger.Error("relay stopped", "error", err)
			return exitCodeError(1)
		}
		return nil
	}
	return cmd
}

type relayConfig struct {
	hostname, stateDir string
	tls, verbose       bool
	tags               []string
	cap                peercap.Cap
	slugs              hooks.SlugConfig
	limits             authz.Limits
	policy             hooks.Policy
	server             server.Config
	revalidate         time.Duration
	opsListen          string // host address of the ops listener; empty: off
	otel, pprof        bool   // --otel, --ops-pprof
	funnel             bool
	funnelPort         int
	funnelMaxBody      int64
}

func runRelay(ctx context.Context, logger *slog.Logger, cfg relayConfig) error {
	// The ops listener starts first, so liveness probes pass while tsnet
	// joins the tailnet. It binds on the host network, never on tsnet.
	ops := &server.Ops{Pprof: cfg.pprof}
	if cfg.opsListen != "" {
		opsLn, err := net.Listen("tcp", cfg.opsListen)
		if err != nil {
			return fmt.Errorf("--ops-listen: %w", err)
		}
		opsSrv := &http.Server{Handler: ops, ReadHeaderTimeout: 10 * time.Second}
		go opsSrv.Serve(opsLn)
		defer opsSrv.Close()
	}

	store, err := hooks.OpenJSON(cfg.stateDir)
	if err != nil {
		return err
	}
	defer store.Close()
	mgr, err := hooks.NewManager(ctx, store, cfg.slugs)
	if err != nil {
		return err
	}
	mgr.SetPolicy(cfg.policy)

	ts := &tsnet.Server{
		Hostname:      cfg.hostname,
		Dir:           filepath.Join(cfg.stateDir, "tsnet"),
		AdvertiseTags: cfg.tags,
		UserLogf:      func(f string, a ...any) { logger.Info(strings.TrimSpace(fmt.Sprintf(f, a...)), "component", "tsnet") },
		Logf:          func(string, ...any) {},
	}
	if cfg.verbose {
		ts.Logf = func(f string, a ...any) { logger.Debug(fmt.Sprintf(f, a...), "component", "tsnet") }
	}
	defer ts.Close()
	st, err := ts.Up(ctx)
	if err != nil {
		return fmt.Errorf("joining the tailnet: %w", err)
	}
	lc, err := ts.LocalClient()
	if err != nil {
		return err
	}

	var ln net.Listener
	scheme := "https"
	if cfg.tls {
		ln, err = ts.ListenTLS("tcp", ":443")
		if err != nil {
			return fmt.Errorf("%w (or run with --tls=false to serve plain HTTP on port 80)", err)
		}
	} else {
		scheme = "http"
		ln, err = ts.Listen("tcp", ":80")
		if err != nil {
			return err
		}
	}
	defer ln.Close()

	dnsName := strings.TrimSuffix(st.Self.DNSName, ".")
	var publicLn net.Listener
	if cfg.funnel {
		publicLn, err = listenFunnel(ts, cfg.funnelPort)
		if err != nil {
			return err
		}
		defer publicLn.Close()
		cfg.server.PublicBaseURL = publicBaseURL(dnsName, cfg.funnelPort)
		cfg.server.PublicMaxBody = cfg.funnelMaxBody
	}
	al := audit.New(logger)
	cfg.server.BaseURL = scheme + "://" + cmp.Or(dnsName, cfg.hostname)
	cfg.server.Version = version
	cfg.server.AllowedHosts = allowedHosts(dnsName, cfg.hostname, st.TailscaleIPs)
	tel, err := telemetry.Setup(ctx, telemetry.Config{OTLP: cfg.otel, Version: version, Hostname: cfg.hostname, Logger: logger})
	if err != nil {
		return fmt.Errorf("--otel: %w", err)
	}
	defer tel.Shutdown(context.Background()) // bounded by telemetry.ShutdownTimeout
	cfg.server.Telemetry = tel
	srv := server.New(cfg.server,
		&identify.Tailscale{Client: lc, Cap: cfg.cap, Defaults: cfg.limits, Logger: logger},
		mgr, al)
	defer srv.Close()
	go srv.RunReaper(ctx, 30*time.Second)
	go srv.RunRevalidator(ctx, cfg.revalidate)

	hs := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	var ps *http.Server
	if publicLn != nil {
		ps = &http.Server{
			Handler:           withFunnelSource(srv.PublicHandler(), logger),
			ConnContext:       funnelConnContext,
			ReadHeaderTimeout: 10 * time.Second,
			ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		}
	}
	ops.Ready(srv)
	logger.Info("relay ready", "url", cfg.server.BaseURL, "cap", string(cfg.cap), "stateDir", cfg.stateDir, "publicUrl", cfg.server.PublicBaseURL)
	errc := make(chan error, 2)
	go func() { errc <- hs.Serve(ln) }()
	if ps != nil {
		go func() { errc <- ps.Serve(publicLn) }()
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Close() // hijacked tunnels are not covered by Shutdown
	if ps != nil {
		if err := ps.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
	}
	if err := hs.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

// allowedHosts lists the Host header values the relay answers to. Empty
// names are dropped: an empty DNS name would otherwise admit requests
// that carry no Host header at all.
func allowedHosts(dnsName, hostname string, ips []netip.Addr) []string {
	var out []string
	for _, h := range []string{dnsName, hostname} {
		if h != "" {
			out = append(out, h)
		}
	}
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out
}

func defaultStateDir() string {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" || fileExists("/.dockerenv") {
		return "/var/lib/tsrelay"
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "tsrelay")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "tsrelay")
	}
	return "/var/lib/tsrelay"
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func newLogger(w io.Writer, format, level string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("--log-level: %w", err)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch format {
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	}
	return nil, fmt.Errorf(`--log-format must be "json" or "text", not %q`, format)
}
