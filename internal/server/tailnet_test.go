//go:build tailnet

package server

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/client"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"tailscale.com/ipn/store/mem"
	"tailscale.com/net/netns"
	"tailscale.com/tailcfg"
	"tailscale.com/tailcfg/peercap"
	"tailscale.com/tsnet"
	"tailscale.com/tstest/integration"
	"tailscale.com/tstest/integration/testcontrol"
	"tailscale.com/types/logger"
)

const capName peercap.Cap = "mazetti.me/cap/relay"

func startNode(t *testing.T, ctx context.Context, controlURL, hostname string) *tsnet.Server {
	t.Helper()
	s := &tsnet.Server{
		Dir:        filepath.Join(t.TempDir(), hostname),
		ControlURL: controlURL,
		Hostname:   hostname,
		Store:      new(mem.Store),
		Ephemeral:  true,
		Logf:       logger.Discard,
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestOverRealTailnet runs the relay and a developer machine as real tsnet
// nodes against an in-process control server that grants the relay
// capability, proving that WhoIs delivers our CapMap as the design assumes.
func TestOverRealTailnet(t *testing.T) {
	netns.SetEnabled(false) // as tsnet's own tests do
	t.Cleanup(func() { netns.SetEnabled(true) })
	derpMap := integration.RunDERPAndSTUN(t, logger.Discard, "127.0.0.1")
	control := &testcontrol.Server{
		DERPMap:        derpMap,
		DNSConfig:      &tailcfg.DNSConfig{Proxied: true},
		MagicDNSDomain: "tail-scale.ts.net",
		Logf:           logger.Discard,
	}
	control.HTTPTestServer = httptest.NewUnstartedServer(control)
	control.HTTPTestServer.Start()
	t.Cleanup(control.HTTPTestServer.Close)
	control.SetGlobalAppCaps(tailcfg.PeerCapMap{capName: {`{"roles":["receiver","sender"],"channels":["*"]}`}})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	relayNode := startNode(t, ctx, control.HTTPTestServer.URL, "tsrelay")
	laptop := startNode(t, ctx, control.HTTPTestServer.URL, "laptop")

	lc, err := relayNode.LocalClient()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := relayNode.Listen("tcp", ":80")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ip4, _ := relayNode.TailscaleIPs()
	base := "http://" + ip4.String()

	store, err := hooks.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mgr, _ := hooks.NewManager(ctx, store, hooks.SlugConfig{})
	discard := slog.New(slog.DiscardHandler)
	srv := New(Config{BaseURL: base, AllowedHosts: []string{ip4.String()}},
		&identify.Tailscale{Client: lc, Cap: capName, Defaults: authz.DefaultLimits, Logger: discard},
		mgr, audit.New(discard))
	defer srv.Close()
	go http.Serve(ln, srv.Handler())

	hc := tunnel.NewHTTPClient(laptop.Dial)
	relayAPI := &client.API{BaseURL: base, HTTP: hc}

	me, err := relayAPI.Me(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if me.Login == "" || me.Node != "laptop" || len(me.Roles[authz.RoleReceiver]) == 0 || len(me.Roles[authz.RoleSender]) == 0 {
		t.Fatalf("WhoIs did not carry the capability as designed: %+v", me)
	}

	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "hello from "+r.Header.Get("Tsrelay-Sender-Node"))
	}))
	defer local.Close()
	target, _ := url.Parse(local.URL)

	hook, err := relayAPI.CreateHook(ctx, hooks.CreateRequest{Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	connected := make(chan struct{}, 4)
	tc := &tunnel.Client{
		Hooks: []string{hook.ID},
		Dial: func(ctx context.Context, ids []string, a tunnel.Attach) (net.Conn, error) {
			return tunnel.Dial(ctx, hc, relayAPI.TunnelURL(a, ids...))
		},
		Handler: client.NewForwarder(target, client.Options{}),
		OnState: func(c bool, _ error) {
			if c {
				connected <- struct{}{}
			}
		},
	}
	go tc.Run(ctx)
	select {
	case <-connected:
	case <-ctx.Done():
		t.Fatal("tunnel never connected over the tailnet")
	}

	// The laptop also holds the sender role here, so it can call its own hook
	// across the tailnet the way a production node would.
	var body string
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := hc.Post(hook.URL+"/done", "text/plain", strings.NewReader("ping"))
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				body = string(b)
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("callback never succeeded: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if body != "hello from laptop" {
		t.Fatalf("body = %q", body)
	}
}
