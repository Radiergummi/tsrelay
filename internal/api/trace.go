package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/telemetry"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// observed wraps an API route in a server span named after its mux pattern
// and records its latency. Long-lived routes (the event stream, tunnel
// attaches) must not be wrapped: a span or latency of hours means nothing.
func (a *API) observed(latency metric.Float64Histogram, pattern string, h http.Handler) http.Handler {
	if a.Tracer == nil && latency == nil {
		return h
	}
	method, route, _ := strings.Cut(pattern, " ")
	methodAttr, routeAttr := attribute.String("method", method), attribute.String("route", route)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var span trace.Span
		if a.Tracer != nil {
			var ctx context.Context
			ctx, span = a.Tracer.Start(r.Context(), pattern, trace.WithSpanKind(trace.SpanKindServer))
			defer span.End()
			r = r.WithContext(ctx)
		}
		sw := &statusWriter{ResponseWriter: w}
		h.ServeHTTP(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		if span != nil && span.IsRecording() {
			span.SetAttributes(
				attribute.String("http.request.method", method),
				attribute.String("http.route", route),
				attribute.Int("http.response.status_code", status),
			)
			if status >= 500 {
				span.SetStatus(codes.Error, http.StatusText(status))
			}
		}
		if latency != nil {
			latency.Record(r.Context(), time.Since(start).Seconds(), metric.WithAttributes(
				methodAttr, routeAttr, attribute.String("status", strconv.Itoa(status))))
		}
	})
}

// statusWriter notes the status an API handler wrote.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (a *API) latencyHistogram() metric.Float64Histogram {
	if a.Meter == nil {
		return nil
	}
	return telemetry.Must(a.Meter.Float64Histogram("tsrelay_api_request_duration_seconds",
		metric.WithDescription("JSON API requests, by method, route pattern and status; the event stream and tunnel attaches excluded."),
		metric.WithExplicitBucketBoundaries(telemetry.Buckets...)))
}

// startTunnelSpan starts the span of a tunnel attach. Without a tracer it
// returns r's context and a nil span.
func (a *API) startTunnelSpan(r *http.Request) (context.Context, trace.Span) {
	if a.Tracer == nil {
		return r.Context(), nil
	}
	return a.Tracer.Start(r.Context(), "tsrelay.tunnel.attach", trace.WithSpanKind(trace.SpanKindServer))
}

// endAttach records the attach's outcome and ends span. hookIDs are only
// the hooks the request passed its ownership and channel checks for, and
// are recorded only when attached; a refusal names no hooks (DESIGN §13.1).
func (a *API) endAttach(span trace.Span, id authz.Identity, attached bool, hookIDs []string) {
	if span == nil {
		return
	}
	result := "refused"
	attrs := []attribute.KeyValue{
		attribute.String("tsrelay.actor.login", id.Login),
		attribute.String("tsrelay.actor.node", id.NodeName),
	}
	if attached {
		result = "attached"
		attrs = append(attrs, attribute.StringSlice("tsrelay.hook.ids", hookIDs))
	}
	attrs = append(attrs, attribute.String("tsrelay.tunnel.result", result))
	span.SetAttributes(attrs...)
	span.End()
}

// traceDetach records a tunnel's end as its own short trace, linked to its
// attach: a span lasting the tunnel's whole life is useless in most backends.
func (a *API) traceDetach(attachCtx context.Context, t *tunnel.Tunnel, lifetime time.Duration) {
	if a.Tracer == nil {
		return
	}
	_, span := a.Tracer.Start(context.Background(), "tsrelay.tunnel.detach",
		trace.WithLinks(trace.LinkFromContext(attachCtx)))
	span.SetAttributes(
		attribute.String("tsrelay.tunnel.id", t.ID),
		attribute.String("tsrelay.tunnel.reason", t.CloseReason()),
		attribute.Float64("tsrelay.tunnel.lifetime", lifetime.Seconds()),
	)
	span.End()
}
