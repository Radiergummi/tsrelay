package relay

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// callTrace is a call's span and the context carrying it. With tracing off
// span is nil and every method does nothing, without allocating.
type callTrace struct {
	ctx  context.Context
	span trace.Span
}

// startCall starts a call's server span. A sender's call joins the
// sender's trace if h.Propagator is set; an injected or public call never
// joins a trace from its headers.
func (h *Handler) startCall(ctx context.Context, header http.Header, injected bool) callTrace {
	if h.Tracer == nil {
		return callTrace{ctx: ctx}
	}
	if h.Propagator != nil && !injected {
		ctx = h.Propagator.Extract(ctx, propagation.HeaderCarrier(header))
	}
	ctx, span := h.Tracer.Start(ctx, "tsrelay.call", trace.WithSpanKind(trace.SpanKindServer))
	return callTrace{ctx: ctx, span: span}
}

// child starts a span below c; end it with end.
func (c callTrace) child(tracer trace.Tracer, name string) callTrace {
	if c.span == nil {
		return c
	}
	ctx, span := tracer.Start(c.ctx, name)
	return callTrace{ctx: ctx, span: span}
}

func (c callTrace) event(name string) {
	if c.span != nil {
		c.span.AddEvent(name)
	}
}

// end ends c's span, recording attr first if hasAttr. A fixed argument
// instead of a variadic one, so calling it with tracing off never
// allocates a backing array for the call that escapes into SetAttributes.
func (c callTrace) end(attr attribute.KeyValue, hasAttr bool) {
	if c.span == nil {
		return
	}
	if hasAttr {
		c.span.SetAttributes(attr)
	}
	c.span.End()
}

// finish records res on the call span and ends it: the same fields as the
// call's audit record, and never a query string, header value or body.
// The span is an error only if the relay produced the outcome (a
// Tsrelay-Error code); a sender that went away, or the app's own 5xx, is
// not.
func (c callTrace) finish(res Result) {
	if c.span == nil {
		return
	}
	if c.span.IsRecording() {
		kv := []attribute.KeyValue{
			attribute.String("tsrelay.call.id", res.CallID),
			attribute.String("tsrelay.call.kind", string(res.Kind)),
			attribute.String("http.request.method", res.Method),
			attribute.String("url.path", res.Path),
			attribute.String("tsrelay.sender.node", res.Sender.NodeName),
			attribute.Int64("tsrelay.bytes_in", res.BytesIn),
			attribute.Int64("tsrelay.bytes_out", res.BytesOut),
		}
		if res.HookID != "" {
			kv = append(kv, attribute.String("tsrelay.hook.id", string(res.HookID)))
		}
		if len(res.Sender.Tags) > 0 {
			kv = append(kv, attribute.String("tsrelay.sender.tags", strings.Join(res.Sender.Tags, ",")))
		}
		if res.Status != 0 {
			kv = append(kv, attribute.Int("http.response.status_code", res.Status))
		}
		if res.Waited > 0 {
			kv = append(kv, attribute.Float64("tsrelay.waited", res.Waited.Seconds()))
		}
		if res.Code != "" {
			kv = append(kv, attribute.String("tsrelay.error", res.Code))
			if res.Code != OutcomeSenderCanceled {
				c.span.SetStatus(codes.Error, res.Code)
			}
		}
		c.span.SetAttributes(kv...)
	}
	c.span.End()
}

// forwardTransport sends a call down its tunnel and notes when the app's
// response headers arrived, which ends the call's time to first byte.
// With a tracer it wraps the round trip in a client span, and with a
// propagator it passes that span's trace context to the app, replacing
// the sender's. The reverse proxy calls it on the handler's goroutine, so
// headers needs no lock.
type forwardTransport struct {
	rt      http.RoundTripper
	headers *time.Time
	tracer  trace.Tracer                  // nil: no span
	prop    propagation.TextMapPropagator // nil: forward trace headers untouched
}

func (t forwardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var span trace.Span
	if t.tracer != nil {
		var ctx context.Context
		ctx, span = t.tracer.Start(req.Context(), "tsrelay.forward", trace.WithSpanKind(trace.SpanKindClient))
		defer span.End()
		if t.prop != nil {
			req = req.Clone(ctx) // a RoundTripper must not modify its request
			req.Header.Del("Traceparent")
			req.Header.Del("Tracestate")
			t.prop.Inject(ctx, propagation.HeaderCarrier(req.Header))
		} else {
			req = req.WithContext(ctx)
		}
	}
	resp, err := t.rt.RoundTrip(req)
	if err == nil {
		*t.headers = time.Now()
	} else if span != nil && req.Context().Err() == nil {
		// The sender going away is not the app's (or the relay's) failure,
		// consistent with the call span's sender_canceled exemption.
		span.SetStatus(codes.Error, "no response from the app")
	}
	return resp, err
}
