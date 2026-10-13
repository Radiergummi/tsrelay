package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/telemetry"
)

const streamChunks, streamChunk = 16, 64 << 10 // a streamed response: 1 MiB in flushed chunks

// benchApp is a local app that reads the request body and answers 204, or
// with stream, streams a 1 MiB response in flushed chunks.
func benchApp(tb testing.TB, stream bool) string {
	chunk := bytes.Repeat([]byte("x"), streamChunk)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if !stream {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		for range streamChunks {
			w.Write(chunk)
			w.(http.Flusher).Flush()
		}
	}))
	tb.Cleanup(srv.Close)
	return srv.URL
}

// benchEnv is a quiet environment with one hook attached to app. With
// tracing, spans go to an SDK tracer whose exporter discards them.
func benchEnv(b *testing.B, tracing bool, app string) (*env, string) {
	cfg := Config{}
	if tracing {
		tel := telemetry.Must(telemetry.Setup(context.Background(), telemetry.Config{}))
		tel.Tracer = sdktrace.NewTracerProvider(sdktrace.WithBatcher(tracetest.NewNoopExporter()))
		tel.Tracing = true
		cfg.Telemetry = tel
	}
	e := newQuietEnv(b, cfg)
	h, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{
		Path:     "bench",
		Settings: hooks.Settings{RateLimit: &hooks.RateLimit{PerSecond: 1e6, Burst: 1_000_000}},
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(e.cli(h.ID, app))
	return e, h.URL
}

func doCall(b *testing.B, c *http.Client, url string, body []byte) {
	resp, err := c.Post(url, "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		b.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		b.Fatalf("status %d", resp.StatusCode)
	}
}

// BenchmarkCall measures a call through the whole relay: sender, relay,
// tunnel, CLI client and app, over loopback. Compare with BenchmarkDirect.
func BenchmarkCall(b *testing.B) {
	for _, tracing := range []bool{false, true} {
		for _, tc := range []struct {
			name   string
			size   int
			stream bool
		}{{"empty", 0, false}, {"1KiB", 1 << 10, false}, {"1MiB", 1 << 20, false}, {"stream1MiB", 0, true}} {
			b.Run(fmt.Sprintf("tracing=%v/%s", tracing, tc.name), func(b *testing.B) {
				e, url := benchEnv(b, tracing, benchApp(b, tc.stream))
				c := e.fake.Client(sender)
				body := bytes.Repeat([]byte("x"), tc.size)
				b.SetBytes(int64(tc.size + streamChunks*streamChunk*btoi(tc.stream)))
				b.ReportAllocs()
				for b.Loop() {
					doCall(b, c, url, body)
				}
			})
		}
	}
}

// BenchmarkDirect is BenchmarkCall's app without the relay: the floor the
// relay's overhead is measured against.
func BenchmarkDirect(b *testing.B) {
	for _, tc := range []struct {
		name   string
		size   int
		stream bool
	}{{"empty", 0, false}, {"1KiB", 1 << 10, false}, {"1MiB", 1 << 20, false}, {"stream1MiB", 0, true}} {
		b.Run(tc.name, func(b *testing.B) {
			url := benchApp(b, tc.stream)
			body := bytes.Repeat([]byte("x"), tc.size)
			b.SetBytes(int64(tc.size + streamChunks*streamChunk*btoi(tc.stream)))
			b.ReportAllocs()
			for b.Loop() {
				doCall(b, http.DefaultClient, url, body)
			}
		})
	}
}

func btoi(v bool) int {
	if v {
		return 1
	}
	return 0
}
