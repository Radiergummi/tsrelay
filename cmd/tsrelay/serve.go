package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/relay"
	"github.com/Radiergummi/tsrelay/internal/server"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	_ "tailscale.com/feature/oauthkey" // lets TS_CLIENT_SECRET mint auth keys
	"tailscale.com/tailcfg/peercap"
	"tailscale.com/tsnet"
)

func serve(ctx context.Context, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("tsrelay serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
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
	legacyResponseTimeout := fs.Duration("response-timeout", 0, "deprecated: use --hook-response-timeout-default")
	concurrency := fs.Int("tunnel-concurrency", tunnel.DefaultConcurrency, "concurrent calls per tunnel")
	revalidate := fs.Duration("revalidate-interval", time.Minute, "how often attached tunnels are re-authorized")
	holdQueue := fs.Int("hold-queue", relay.DefaultHoldQueue, "calls per hook that may wait for a tunnel at once")
	logFormat := fs.String("log-format", "json", `"json" or "text"`)
	logLevel := fs.String("log-level", "info", "debug, info, warn or error")
	verbose := fs.Bool("verbose-tailscale", false, "include tsnet's own logs")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := applyEnv(fs, "TSRELAY_"); err != nil {
		fmt.Fprintln(stderr, "tsrelay serve:", err)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "tsrelay serve: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if *slugMode != string(hooks.SlugLocalPart) && *slugMode != string(hooks.SlugLogin) {
		fmt.Fprintf(stderr, "tsrelay serve: --user-slug must be %q or %q\n", hooks.SlugLocalPart, hooks.SlugLogin)
		return 2
	}
	if *revalidate <= 0 {
		fmt.Fprintln(stderr, "tsrelay serve: --revalidate-interval must be positive")
		return 2
	}
	if *holdQueue < 1 {
		fmt.Fprintln(stderr, "tsrelay serve: --hold-queue must be at least 1; use --hook-hold-timeout-default=0s to disable holding")
		return 2
	}
	for login, slug := range overrides {
		if !hooks.ValidSlug(slug) {
			fmt.Fprintf(stderr, "tsrelay serve: --user-slug-override %s=%s: a slug may only contain a-z, 0-9, '.', '_' and '-'\n", login, slug)
			return 2
		}
	}
	logger, err := newLogger(stderr, *logFormat, *logLevel)
	if err != nil {
		fmt.Fprintln(stderr, "tsrelay serve:", err)
		return 2
	}
	if wasSet(fs, "response-timeout") {
		logger.Warn("--response-timeout is deprecated; use --hook-response-timeout-default")
		if !wasSet(fs, "hook-response-timeout-default") {
			_ = fs.Set("hook-response-timeout-default", legacyResponseTimeout.String())
		}
	}
	policy, err := policyFlags()
	if err != nil {
		fmt.Fprintln(stderr, "tsrelay serve:", err)
		return 2
	}

	err = runRelay(ctx, logger, relayConfig{
		hostname: *hostname, stateDir: *stateDir, tls: *useTLS, tags: tags.Values, verbose: *verbose,
		cap:        peercap.Cap(*capName),
		slugs:      hooks.SlugConfig{Mode: hooks.SlugMode(*slugMode), Overrides: overrides},
		limits:     authz.Limits{MaxHooks: *maxHooks, MaxTunnels: *maxTunnels, MaxHookTTL: *maxTTL},
		policy:     policy,
		server:     server.Config{EphemeralGrace: *grace, HoldQueue: *holdQueue, Tunnel: tunnel.Options{Concurrency: *concurrency}},
		revalidate: *revalidate,
	})
	if err != nil {
		logger.Error("relay stopped", "error", err)
		return 1
	}
	return 0
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
}

func runRelay(ctx context.Context, logger *slog.Logger, cfg relayConfig) error {
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
	al := audit.New(logger)
	cfg.server.BaseURL = scheme + "://" + cmp.Or(dnsName, cfg.hostname)
	cfg.server.AllowedHosts = allowedHosts(dnsName, cfg.hostname, st.TailscaleIPs)
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
	logger.Info("relay ready", "url", cfg.server.BaseURL, "cap", string(cfg.cap), "stateDir", cfg.stateDir)
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Close() // hijacked tunnels are not covered by Shutdown
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
