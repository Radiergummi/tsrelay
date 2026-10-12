package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/Radiergummi/tsrelay/internal/problem"
	"golang.org/x/net/http2"
)

// NewHTTPClient returns a client suitable for Dial and for the relay API:
// HTTP/1.1 only, and without an overall timeout that would cut the tunnel.
// dial may be nil to use the system network.
func NewHTTPClient(dial func(ctx context.Context, network, addr string) (net.Conn, error)) *http.Client {
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	var protos http.Protocols
	protos.SetHTTP1(true)
	return &http.Client{Transport: &http.Transport{
		DialContext:         dial,
		Protocols:           &protos,
		TLSHandshakeTimeout: 10 * time.Second,
	}}
}

// Dial performs the upgrade handshake against endpoint, the full tunnel URL
// including its hook query parameters. A refusal is returned as a
// *problem.Problem when the relay sent one.
func Dial(ctx context.Context, hc *http.Client, endpoint string) (net.Conn, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", UpgradeToken)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		return nil, problem.Decode(resp)
	}
	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		resp.Body.Close()
		return nil, errors.New("tunnel: relay connection is not writable after upgrade")
	}
	return rwcConn{rwc}, nil
}

// Serve speaks HTTP/2, as server, on conn until conn fails or ctx is done.
// Control messages go to onControl (which may be nil); everything else to h.
func Serve(ctx context.Context, conn net.Conn, h http.Handler, onControl func(Control)) {
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	srv := &http2.Server{
		MaxConcurrentStreams: DefaultConcurrency,
		ReadIdleTimeout:      pingInterval,
		PingTimeout:          pingInterval,
	}
	srv.ServeConn(conn, &http2.ServeConnOpts{
		Context: ctx,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(HeaderControl) == "" {
				h.ServeHTTP(w, r)
				return
			}
			var c Control
			if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if onControl != nil {
				onControl(c)
			}
			w.WriteHeader(http.StatusNoContent)
		}),
	})
}
