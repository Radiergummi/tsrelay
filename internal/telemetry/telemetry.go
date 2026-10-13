// Package telemetry sets up OpenTelemetry for the relay (DESIGN.md §13):
// metrics always, served as Prometheus text on the ops listener, and,
// with OTLP on, traces plus metric export over OTLP/HTTP, configured by
// the standard OTEL_* environment variables. Other packages use only the
// providers and the propagator it returns, never the OTel globals.
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/otlptranslator"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// ScopeName is the instrumentation scope of every tracer and meter.
const ScopeName = "github.com/Radiergummi/tsrelay"

// ShutdownTimeout bounds how long Shutdown waits to flush.
const ShutdownTimeout = 5 * time.Second

// Buckets are the boundaries, in seconds, of every latency histogram.
var Buckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120}

type Config struct {
	OTLP     bool         // export traces and metrics over OTLP/HTTP
	Version  string       // service.version
	Hostname string       // host.name
	Logger   *slog.Logger // receives SDK errors, at most one per minute; nil: slog.Default()
}

type Telemetry struct {
	Tracing    bool // spans are recorded and exported; false: Tracer is a noop
	Tracer     trace.TracerProvider
	Meter      metric.MeterProvider
	Propagator propagation.TextMapPropagator
	Metrics    http.Handler // Prometheus text exposition of Meter

	shutdown []func(context.Context) error
}

func Setup(ctx context.Context, cfg Config) (*Telemetry, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	otel.SetErrorHandler(rateLimited(logger, time.Minute))

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName("tsrelay"),
		semconv.ServiceVersion(cfg.Version),
		semconv.HostName(cfg.Hostname),
	))
	if err != nil {
		return nil, err
	}
	// resource.Default already reads OTEL_SERVICE_NAME and
	// OTEL_RESOURCE_ATTRIBUTES; let them win over the values above.
	if env, err := resource.New(ctx, resource.WithFromEnv()); err == nil {
		res, _ = resource.Merge(res, env)
	}

	t := &Telemetry{
		Propagator: propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}),
		Tracer:     noop.NewTracerProvider(),
	}

	reg := prometheus.NewRegistry()
	prom, err := otelprom.New(
		otelprom.WithRegisterer(reg),
		otelprom.WithTranslationStrategy(otlptranslator.UnderscoreEscapingWithoutSuffixes),
		otelprom.WithoutScopeInfo(),
		otelprom.WithoutTargetInfo(),
	)
	if err != nil {
		return nil, err
	}
	readers := []sdkmetric.Option{sdkmetric.WithResource(res), sdkmetric.WithReader(prom)}

	if cfg.OTLP {
		mexp, err := otlpmetrichttp.New(ctx)
		if err != nil {
			return nil, err
		}
		readers = append(readers, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(mexp)))
		texp, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, err
		}
		tp := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(texp))
		t.Tracer, t.Tracing = tp, true
		t.shutdown = append(t.shutdown, tp.Shutdown)
	}
	mp := sdkmetric.NewMeterProvider(readers...)
	t.Meter = mp
	t.shutdown = append(t.shutdown, mp.Shutdown)
	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		return nil, err
	}
	t.Metrics = promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	return t, nil
}

// Must returns v, or panics if err is set. For what fails only on
// programming errors: a setup without OTLP, an instrument with a constant name.
func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// Shutdown flushes pending spans and metrics, bounded by ShutdownTimeout.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, ShutdownTimeout)
	defer cancel()
	var errs []error
	for _, f := range t.shutdown {
		errs = append(errs, f(ctx))
	}
	return errors.Join(errs...)
}

// rateLimited logs SDK errors, such as an unreachable collector, at most
// once per interval, so a dead endpoint does not flood the log.
func rateLimited(l *slog.Logger, interval time.Duration) otel.ErrorHandlerFunc {
	var mu sync.Mutex
	var last time.Time
	return func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if now := time.Now(); now.Sub(last) >= interval {
			last = now
			l.Warn("telemetry export failed", "error", err)
		}
	}
}
