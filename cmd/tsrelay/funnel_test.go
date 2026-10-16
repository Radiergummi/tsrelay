package main

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"tailscale.com/ipn"
)

func TestPublicBaseURL(t *testing.T) {
	for port, want := range map[int]string{
		443:   "https://tsrelay.example.ts.net",
		8443:  "https://tsrelay.example.ts.net:8443",
		10000: "https://tsrelay.example.ts.net:10000",
	} {
		if got := publicBaseURL("tsrelay.example.ts.net", port); got != want {
			t.Errorf("port %d: %q, want %q", port, got, want)
		}
	}
}

func TestFunnelSourceBecomesRemoteAddr(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	src := netip.MustParseAddrPort("203.0.113.7:4242")
	conn := tls.Server(&ipn.FunnelConn{Conn: a, Src: src}, &tls.Config{})
	ctx := funnelConnContext(context.Background(), conn)
	var got string
	h := withFunnelSource(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.RemoteAddr }),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/+t/x", nil).WithContext(ctx))
	if got != "203.0.113.7:4242" {
		t.Fatalf("RemoteAddr = %q", got)
	}
}

func TestNonFunnelConnIsDropped(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ctx := funnelConnContext(context.Background(), tls.Server(a, &tls.Config{}))
	called := false
	h := withFunnelSource(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer func() {
		if p := recover(); p != http.ErrAbortHandler || called {
			t.Fatalf("recover = %v, handler called = %v; want the request aborted", p, called)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/+t/x", nil).WithContext(ctx))
}
