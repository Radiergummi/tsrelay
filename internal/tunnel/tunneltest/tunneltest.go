// Package tunneltest creates in-process tunnels for tests.
package tunneltest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/ids"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// Pair opens a tunnel over a real loopback connection whose client end
// serves local. drop closes the client end, simulating a lost tunnel.
func Pair(t testing.TB, local http.Handler, opts tunnel.Options, onControl func(tunnel.Control)) (tun *tunnel.Tunnel, drop func()) {
	t.Helper()
	tunnels := make(chan *tunnel.Tunnel, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := tunnel.Accept(w, r)
		if err != nil {
			t.Error(err)
			return
		}
		tun, err := tunnel.NewTunnel(conn, ids.New("t_"), authz.Identity{}, opts)
		if err != nil {
			t.Error(err)
			return
		}
		tunnels <- tun
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	conn, err := tunnel.Dial(ctx, tunnel.NewHTTPClient(nil), srv.URL)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	go tunnel.Serve(ctx, conn, local, onControl)
	tun = <-tunnels
	t.Cleanup(func() {
		cancel()
		tun.Close()
	})
	return tun, cancel
}
