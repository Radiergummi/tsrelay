package telemetry

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("/metrics = %d", rec.Code)
	}
	return rec.Body.String()
}

// The exporter must keep instrument names exactly as given: no unit or
// _total suffixes added, no otel_scope_* labels, no target_info.
func TestPrometheusNames(t *testing.T) {
	tel := Must(Setup(context.Background(), Config{}))
	defer tel.Shutdown(context.Background())
	m := tel.Meter.Meter(ScopeName)
	c, _ := m.Float64Counter("tsrelay_calls_total", metricDescription("Calls."))
	c.Add(context.Background(), 1, withAttr("outcome", "ok"))
	h, _ := m.Float64Histogram("tsrelay_call_duration_seconds", histogramBuckets())
	h.Record(context.Background(), 0.02, withAttr("outcome", "ok"))

	body := scrape(t, tel.Metrics)
	for _, want := range []string{
		`tsrelay_calls_total{outcome="ok"} 1`,
		`tsrelay_call_duration_seconds_bucket{outcome="ok",le="0.025"} 1`,
		`tsrelay_call_duration_seconds_count{outcome="ok"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q:\n%s", want, body)
		}
	}
	for _, bad := range []string{"otel_scope", "target_info", "tsrelay_calls_total_total", "seconds_seconds"} {
		if strings.Contains(body, bad) {
			t.Errorf("metrics contain %q:\n%s", bad, body)
		}
	}
	// Standard dashboards and alerts look for these names.
	for _, want := range []string{"go_goroutine", "go_goroutines ", "go_memstats_heap_inuse_bytes ", "process_start_time_seconds "} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q:\n%s", want, body)
		}
	}
}

// collector is a fake OTLP/HTTP endpoint that counts requests per path.
type collector struct {
	mu    sync.Mutex
	paths map[string]int
}

func newCollector(t *testing.T) (*collector, string) {
	c := &collector{paths: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		c.mu.Lock()
		c.paths[r.URL.Path]++
		c.mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return c, srv.URL
}

func (c *collector) count(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paths[path]
}

func TestOTLPOffDialsNothing(t *testing.T) {
	c, url := newCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", url)
	tel := Must(Setup(context.Background(), Config{}))
	if tel.Tracing {
		t.Fatal("Tracing is on without OTLP")
	}
	_, span := tel.Tracer.Tracer(ScopeName).Start(context.Background(), "x")
	if span.IsRecording() {
		t.Fatal("span records without OTLP")
	}
	span.End()
	tel.Shutdown(context.Background())
	if n := c.count("/v1/traces") + c.count("/v1/metrics"); n != 0 {
		t.Fatalf("collector got %d requests with OTLP off", n)
	}
}

func TestOTLPOnExports(t *testing.T) {
	c, url := newCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", url)
	tel := Must(Setup(context.Background(), Config{OTLP: true, Version: "v9", Hostname: "relay"}))
	if !tel.Tracing {
		t.Fatal("Tracing is off with OTLP")
	}
	_, span := tel.Tracer.Tracer(ScopeName).Start(context.Background(), "x")
	span.End()
	ctr, _ := tel.Meter.Meter(ScopeName).Int64Counter("tsrelay_test_total")
	ctr.Add(context.Background(), 1)
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.count("/v1/traces") == 0 || c.count("/v1/metrics") == 0 {
		t.Fatalf("collector got %v, want traces and metrics", c.paths)
	}
}

func TestSetupUnreachableCollector(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1") // nothing listens
	var logs bytes.Buffer
	LogErrors(slog.New(slog.NewTextHandler(&logs, nil)))
	tel := Must(Setup(context.Background(), Config{OTLP: true}))
	for range 3 {
		_, span := tel.Tracer.Tracer(ScopeName).Start(context.Background(), "x")
		span.End()
	}
	ctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout+time.Second)
	defer cancel()
	start := time.Now()
	tel.Shutdown(ctx) // an error is fine; hanging is not
	if d := time.Since(start); d > ShutdownTimeout+time.Second {
		t.Fatalf("Shutdown took %v", d)
	}
	if n := strings.Count(logs.String(), "telemetry export failed"); n > 1 {
		t.Fatalf("logged %d export failures within a minute, want at most 1:\n%s", n, logs.String())
	}
}

func metricDescription(s string) metric.Float64CounterOption { return metric.WithDescription(s) }
func withAttr(k, v string) metric.MeasurementOption {
	return metric.WithAttributes(attribute.String(k, v))
}
func histogramBuckets() metric.Float64HistogramOption {
	return metric.WithExplicitBucketBoundaries(Buckets...)
}
