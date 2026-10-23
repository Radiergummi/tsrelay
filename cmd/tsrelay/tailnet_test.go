//go:build tailnet

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"tailscale.com/ipn/store/mem"
	"tailscale.com/net/netns"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
	"tailscale.com/tstest/integration"
	"tailscale.com/tstest/integration/testcontrol"
	"tailscale.com/types/logger"
)

// TestOnATailnet runs the relay and the CLI as they run for real, as
// tsnet nodes of a tailnet, with Tailscale's own test control server and
// DERP: identities come from WhoIs and grants from the capability map
// control sends, and calls travel over WireGuard. It covers what the
// in-process tests can't: joining, the relay's tailnet listener, a tunnel
// over the tailnet, a shutdown that drains, and a restart on the same
// state. Funnel needs Tailscale's ingress, which this can't cover.
// internal/server's TestOverRealTailnet covers the server alone; this runs
// the commands. Run with -tags tailnet.
func TestOnATailnet(t *testing.T) {
	netns.SetEnabled(false)
	t.Cleanup(func() { netns.SetEnabled(true) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	control := &testcontrol.Server{
		DERPMap:        integration.RunDERPAndSTUN(t, logger.Discard, "127.0.0.1"),
		DNSConfig:      &tailcfg.DNSConfig{Proxied: true},
		MagicDNSDomain: "tail-scale.ts.net",
		TagOwners:      map[string][]string{"tag:tsrelay": nil, "tag:prod-worker": nil},
		Logf:           logger.Discard,
	}
	control.HTTPTestServer = httptest.NewUnstartedServer(control)
	control.HTTPTestServer.Start()
	t.Cleanup(control.HTTPTestServer.Close)
	// As a policy granting every device the receiver and sender roles.
	control.SetGlobalAppCaps(tailcfg.PeerCapMap{
		"tsrelay.mazetti.me/cap/relay": {tailcfg.RawMessage(`{"roles":["receiver","sender"]}`)},
	})
	t.Setenv("TS_CONTROL_URL", control.HTTPTestServer.URL) // for the relay's tsnet

	node := func(name string, tags ...string) *tsnet.Server {
		s := &tsnet.Server{Dir: t.TempDir(), ControlURL: control.HTTPTestServer.URL, Hostname: name,
			Store: new(mem.Store), Ephemeral: true, AdvertiseTags: tags, Logf: logger.Discard, UserLogf: logger.Discard}
		t.Cleanup(func() { s.Close() })
		if _, err := s.Up(ctx); err != nil {
			t.Fatal(err)
		}
		return s
	}
	laptop, worker := node("laptop"), node("worker", "tag:prod-worker")
	asLaptop, asWorker := tunnel.NewHTTPClient(laptop.Dial), tunnel.NewHTTPClient(worker.Dial)
	const relayURL = "http://tsrelay.tail-scale.ts.net"

	state := t.TempDir()
	startRelay := func() (stop func() int) {
		var log syncBuf
		rctx, stopRelay := context.WithCancel(ctx)
		exit := make(chan int, 1)
		go func() {
			exit <- run(rctx, []string{"serve", "--tls=false", "--state-dir", state, "--update-check=false",
				"--log-format", "text"}, io.Discard, &log, deps{})
		}()
		waitUntil(t, ctx, "the relay is ready", func() bool { return strings.Contains(log.String(), "relay ready") })
		// The first packets wait for the peers' DERP connections.
		waitUntil(t, ctx, "the laptop reaches the relay", func() bool {
			resp, err := asLaptop.Get(relayURL + "/")
			if err == nil {
				resp.Body.Close()
			}
			return err == nil && resp.StatusCode == 200
		})
		return func() int {
			stopRelay()
			select {
			case code := <-exit:
				return code
			case <-ctx.Done():
				t.Fatalf("the relay didn't stop:\n%s", log.String())
				return -1
			}
		}
	}
	stopRelay := startRelay()

	// WhoIs and the grant, as the relay reads them.
	var me api.Me
	getJSON(t, asLaptop, relayURL+"/", &me)
	if me.Login == "" || me.Node != "laptop" || len(me.Roles["receiver"]) == 0 || len(me.Roles["sender"]) == 0 {
		t.Fatalf("GET / as the laptop = %+v", me)
	}

	// tsrelay http, as a developer runs it, over the laptop's tailnet node.
	var slowArrived atomic.Bool
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			slowArrived.Store(true)
			time.Sleep(time.Second)
		}
		fmt.Fprintf(w, "%s from %s %s", r.URL.Path, r.Header.Get("Tsrelay-Sender-Node"), r.Header.Get("Tsrelay-Sender-Tags"))
	}))
	defer local.Close()
	var cliOut, cliErr syncBuf
	cliCtx, stopCLI := context.WithCancel(ctx)
	cliDone := make(chan int, 1)
	go func() {
		cliDone <- run(cliCtx, []string{"http", local.URL, "--relay", relayURL, "--path", "pay", "--json"},
			&cliOut, &cliErr, deps{HTTP: asLaptop, StatePath: t.TempDir() + "/state.json"})
	}()
	defer func() { stopCLI(); <-cliDone }()
	var hookURL string
	waitUntil(t, ctx, "tsrelay http serves the hook", func() bool {
		hookURL = jsonEventField(cliOut.String(), "url", "url")
		return hookURL != ""
	})

	// A call from a tagged production node, over the tailnet.
	if got := post(ctx, asWorker, hookURL+"/done"); got != "200 /done from worker tag:prod-worker" {
		t.Fatalf("call = %q\nCLI: %s", got, cliErr.String())
	}

	// A shutdown answers a held call at once and lets a delivered one finish.
	var held api.Hook
	postJSON(t, asLaptop, relayURL+"/hooks", `{"path":"held"}`, &held)
	heldResult, slowResult := make(chan string, 1), make(chan string, 1)
	go func() { heldResult <- post(ctx, asWorker, held.URL) }()
	go func() { slowResult <- post(ctx, asWorker, hookURL+"/slow") }()
	waitUntil(t, ctx, "the slow call reaches the app", slowArrived.Load)
	time.Sleep(200 * time.Millisecond) // and the held call waits for a tunnel
	if code := stopRelay(); code != 0 {
		t.Fatalf("the relay exited %d on shutdown", code)
	}
	if got := <-slowResult; got != "200 /slow from worker tag:prod-worker" {
		t.Errorf("the delivered call = %q, want it to finish", got)
	}
	if got := <-heldResult; !strings.HasPrefix(got, "503 ") || !strings.Contains(got, problem.ShuttingDown) {
		t.Errorf("the held call = %q, want 503 %s", got, problem.ShuttingDown)
	}

	// Restarted on its state, the relay keeps the hook, and the CLI comes back.
	stopRelay = startRelay()
	defer stopRelay()
	waitUntil(t, ctx, "the CLI reconnects", func() bool {
		return strings.Contains(cliOut.String(), `"restored":true`)
	})
	if got := post(ctx, asWorker, hookURL+"/again"); got != "200 /again from worker tag:prod-worker" {
		t.Fatalf("call after the restart = %q", got)
	}
}

func waitUntil(t *testing.T, ctx context.Context, what string, cond func() bool) {
	t.Helper()
	for !cond() {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting until %s", what)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func getJSON(t *testing.T, hc *http.Client, url string, out any) {
	t.Helper()
	resp, err := hc.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET %s: %d %v", url, resp.StatusCode, err)
	}
}

func postJSON(t *testing.T, hc *http.Client, url, body string, out any) {
	t.Helper()
	resp, err := hc.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil || resp.StatusCode != 201 {
		t.Fatalf("POST %s: %d %v", url, resp.StatusCode, err)
	}
}

// post sends an empty POST and returns "<status> <body>". It may run on
// another goroutine, so it reports errors in the result.
func post(ctx context.Context, hc *http.Client, url string) string {
	req, _ := http.NewRequestWithContext(ctx, "POST", url, nil)
	resp, err := hc.Do(req)
	if err != nil {
		return err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return fmt.Sprintf("%d %s", resp.StatusCode, b)
}

// jsonEventField is field of the first --json event of type event in out.
func jsonEventField(out, event, field string) string {
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		var e map[string]any
		if json.Unmarshal(sc.Bytes(), &e) == nil && e["event"] == event {
			s, _ := e[field].(string)
			return s
		}
	}
	return ""
}
