package metrics

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestExposition(t *testing.T) {
	r := NewRegistry()
	calls := r.Counter("t_calls_total", "Calls, by outcome.", "outcome")
	calls.Add("ok", 2)
	calls.Add(`we"ird\`+"\n", 1)
	calls.Add("ok", 1)
	r.Counter("t_denials_total", "Never incremented.", "action")
	r.GaugeFunc("t_up", "Up.\nSecond line.", "", func() map[string]float64 { return map[string]float64{"": 1} })
	r.GaugeFunc("t_hooks", "Hooks.", "ephemeral", func() map[string]float64 {
		return map[string]float64{"true": 0, "false": 3}
	})

	var b strings.Builder
	if _, err := r.WriteTo(&b); err != nil {
		t.Fatal(err)
	}
	want := `# HELP t_calls_total Calls, by outcome.
# TYPE t_calls_total counter
t_calls_total{outcome="ok"} 3
t_calls_total{outcome="we\"ird\\\n"} 1
# HELP t_denials_total Never incremented.
# TYPE t_denials_total counter
# HELP t_up Up.\nSecond line.
# TYPE t_up gauge
t_up 1
# HELP t_hooks Hooks.
# TYPE t_hooks gauge
t_hooks{ephemeral="false"} 3
t_hooks{ephemeral="true"} 0
`
	if b.String() != want {
		t.Fatalf("exposition:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestLargeValues(t *testing.T) {
	r := NewRegistry()
	r.Counter("t_bytes_total", "Bytes.", "direction").Add("in", 1<<40)
	var b strings.Builder
	r.WriteTo(&b)
	if !strings.Contains(b.String(), `t_bytes_total{direction="in"} 1.099511627776e+12`) {
		t.Fatalf("exposition = %s", b.String())
	}
}

func TestConcurrentAdds(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("t_total", "T.", "")
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			for range 100 {
				c.Add("", 1)
			}
		})
	}
	wg.Wait()
	var b strings.Builder
	r.WriteTo(&b)
	if !strings.Contains(b.String(), "\nt_total 1000\n") {
		t.Fatalf("exposition = %s", b.String())
	}
}

func TestServeHTTP(t *testing.T) {
	r := NewRegistry()
	r.Counter("t_total", "T.", "").Add("", 1)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "t_total 1\n") {
		t.Errorf("body = %q", rec.Body.String())
	}
}
