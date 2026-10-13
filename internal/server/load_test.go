package server

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
)

// TestLoad drives the relay in-process at a fixed concurrency for a fixed
// time, then the same app directly, and reports what the relay adds. It is
// a measuring tool, not a test: it only fails if calls fail.
//
//	TSRELAY_LOAD=1 go test ./internal/server -run TestLoad -count=1 -v
//
// Knobs: TSRELAY_LOAD_CONCURRENCY (16), TSRELAY_LOAD_DURATION (10s),
// TSRELAY_LOAD_BODY (1024 bytes), TSRELAY_LOAD_MODE (plain or streaming).
// Profile it with -cpuprofile, -memprofile or -trace.
func TestLoad(t *testing.T) {
	if os.Getenv("TSRELAY_LOAD") == "" {
		t.Skip("set TSRELAY_LOAD=1 to run the load harness")
	}
	conc := envInt(t, "TSRELAY_LOAD_CONCURRENCY", 16)
	dur := envDuration(t, "TSRELAY_LOAD_DURATION", 10*time.Second)
	size := envInt(t, "TSRELAY_LOAD_BODY", 1024)
	mode := os.Getenv("TSRELAY_LOAD_MODE")
	if mode == "" {
		mode = "plain"
	}
	if mode != "plain" && mode != "streaming" {
		t.Fatalf("TSRELAY_LOAD_MODE = %q, want plain or streaming", mode)
	}

	app := benchApp(t, mode == "streaming")
	e := newQuietEnv(t, Config{})
	limit := hooks.RateLimit{PerSecond: 1e6, Burst: 1_000_000}
	h, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "load", Settings: hooks.Settings{RateLimit: &limit}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.cli(h.ID, app)()
	body := bytes.Repeat([]byte("x"), size)

	// http.DefaultClient and e.fake.Client both cap idle connections per host
	// at 2; at conc workers hammering one host that thrashes the connection
	// pool and, at this concurrency, can exhaust ephemeral ports outright.
	// Give each worker a connection of its own to keep.
	direct := drive(t, idleClient(conc, nil), app, body, conc, dur)
	relayed := drive(t, idleClient(conc, e.fake.Dial(sender)), h.URL, body, conc, dur)

	t.Logf("mode=%s concurrency=%d duration=%s body=%dB GOMAXPROCS=%d", mode, conc, dur, size, runtime.GOMAXPROCS(0))
	t.Logf("%-8s %10s %10s %10s %10s %12s %10s", "", "calls/s", "p50", "p95", "p99", "allocs/call", "peak gr")
	for _, r := range []struct {
		name string
		res  loadResult
	}{{"direct", direct}, {"relay", relayed}} {
		t.Logf("%-8s %10.0f %10s %10s %10s %12.0f %10d", r.name, r.res.rate, r.res.p(50), r.res.p(95), r.res.p(99), r.res.allocs, r.res.peakGoroutines)
	}
	t.Logf("%-8s %10s %10s %10s %10s", "added", "", relayed.p(50)-direct.p(50), relayed.p(95)-direct.p(95), relayed.p(99)-direct.p(99))
	t.Logf("allocs/call are process-wide: the sending client and the app are included in both rows")
}

type loadResult struct {
	latencies      []time.Duration // sorted
	rate           float64         // calls per second
	allocs         float64         // heap allocations per call, process-wide
	peakGoroutines int
}

// p returns the q-th percentile latency.
func (r loadResult) p(q int) time.Duration {
	if len(r.latencies) == 0 {
		return 0
	}
	return r.latencies[min(len(r.latencies)-1, len(r.latencies)*q/100)]
}

// drive sends calls to url from conc workers until dur has passed.
func drive(t *testing.T, c *http.Client, url string, body []byte, conc int, dur time.Duration) loadResult {
	var (
		mu    sync.Mutex
		all   []time.Duration
		fails atomic.Int64
		peak  atomic.Int64
		wg    sync.WaitGroup
	)
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() { // sample the goroutine count while the run lasts
		defer close(sampled)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			if n := int64(runtime.NumGoroutine()); n > peak.Load() {
				peak.Store(n)
			}
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	}()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	deadline := start.Add(dur)
	for range conc {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var mine []time.Duration
			for time.Now().Before(deadline) {
				t0 := time.Now()
				resp, err := c.Post(url, "application/octet-stream", bytes.NewReader(body))
				if err != nil {
					fails.Add(1)
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode >= 300 {
					fails.Add(1)
					continue
				}
				mine = append(mine, time.Since(t0))
			}
			mu.Lock()
			all = append(all, mine...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	close(stop)
	<-sampled
	if n := fails.Load(); n > 0 {
		t.Errorf("%d calls to %s failed", n, url)
	}
	slices.Sort(all)
	calls := max(1, len(all))
	return loadResult{
		latencies:      all,
		rate:           float64(len(all)) / elapsed.Seconds(),
		allocs:         float64(after.Mallocs-before.Mallocs) / float64(calls),
		peakGoroutines: int(peak.Load()),
	}
}

// idleClient returns an HTTP client that keeps n idle connections per host,
// one per expected concurrent caller, identified by dial (nil for the
// relay's own default dialer).
func idleClient(n int, dial func(ctx context.Context, network, addr string) (net.Conn, error)) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: dial, MaxIdleConnsPerHost: n}}
}

func envInt(t *testing.T, key string, def int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		t.Fatalf("%s = %q: want a positive integer", key, s)
	}
	return n
}

func envDuration(t *testing.T, key string, def time.Duration) time.Duration {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		t.Fatalf("%s = %q: want a positive duration such as 10s", key, s)
	}
	return d
}
