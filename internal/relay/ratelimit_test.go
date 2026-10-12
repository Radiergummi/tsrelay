package relay

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

func TestRateLimit(t *testing.T) {
	e := newEnv(t)
	e.attach(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), tunnel.Options{})
	for i := range 3 {
		if resp := e.do(sender, "POST", "/~moritz/cb", nil, nil); resp.StatusCode != 200 {
			t.Fatalf("call %d under the default limit: %d", i, resp.StatusCode)
		}
	}

	// Lowering the limit applies to the next call, without a restart.
	e.patch(hooks.SettingsPatch{RateLimit: hooks.Opt[hooks.RateLimit]{Set: true, Value: &hooks.RateLimit{PerSecond: 0.1, Burst: 2}}})
	for i := range 2 {
		if resp := e.do(sender, "POST", "/~moritz/cb", nil, nil); resp.StatusCode != 200 {
			t.Fatalf("call %d within the burst: %d", i, resp.StatusCode)
		}
	}
	resp := e.do(sender, "POST", "/~moritz/cb", nil, nil)
	wantProblem(t, resp, 429, problem.RateLimited)
	// One token per 10s: the next one is about 10s away.
	if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err != nil || n < 9 || n > 10 {
		t.Fatalf("Retry-After = %q, want about 10", resp.Header.Get("Retry-After"))
	}
	// Back to the relay default (null) starts a fresh bucket.
	e.patch(hooks.SettingsPatch{RateLimit: hooks.Opt[hooks.RateLimit]{Set: true}})
	if resp := e.do(sender, "POST", "/~moritz/cb", nil, nil); resp.StatusCode != 200 {
		t.Fatalf("after resetting to the default: %d", resp.StatusCode)
	}
}

func TestRetryAfterIsAtLeastOneSecond(t *testing.T) {
	for d, want := range map[time.Duration]string{time.Millisecond: "1", time.Second: "1", 1500 * time.Millisecond: "2"} {
		if got := retryAfter(d); got != want {
			t.Errorf("retryAfter(%v) = %q, want %q", d, got, want)
		}
	}
}

// Buckets of deleted hooks are dropped by a periodic sweep.
func TestLimitersDropDeletedHooks(t *testing.T) {
	var ls limiters
	rl := hooks.RateLimit{PerSecond: 1, Burst: 1}
	exists := map[hooks.ID]bool{"h_a": true, "h_b": true}
	now := time.Unix(1000, 0)
	ls.allow("h_a", rl, now, func(id hooks.ID) bool { return exists[id] })
	if _, ok := ls.allow("h_a", rl, now, func(id hooks.ID) bool { return exists[id] }); ok {
		t.Fatal("the second call within a second must be limited")
	}
	delete(exists, "h_a")
	ls.allow("h_b", rl, now.Add(time.Second), func(id hooks.ID) bool { return exists[id] })
	if len(ls.byHook) != 2 {
		t.Fatalf("swept before the interval: %d buckets", len(ls.byHook))
	}
	ls.allow("h_b", rl, now.Add(sweepInterval), func(id hooks.ID) bool { return exists[id] })
	if _, ok := ls.byHook["h_a"]; ok || len(ls.byHook) != 1 {
		t.Fatalf("buckets after the sweep: %v", ls.byHook)
	}
}
