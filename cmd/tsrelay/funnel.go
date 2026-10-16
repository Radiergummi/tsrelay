package main

// This is the only file that may use tsnet's Funnel APIs
// (TestFunnelConfined): everything the relay exposes to the internet
// starts here.

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"

	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

// listenFunnel opens the Funnel-only listener for public hooks. It shares
// no traffic with the tailnet listener, even on the same port.
func listenFunnel(ts *tsnet.Server, port int) (net.Listener, error) {
	ln, err := ts.ListenFunnel("tcp", ":"+strconv.Itoa(port), tsnet.FunnelOnly())
	if err != nil {
		return nil, fmt.Errorf("--funnel: %w (the tailnet policy must give this node the \"funnel\" node attribute, and HTTPS must be enabled)", err)
	}
	return ln, nil
}

// publicBaseURL is the relay's URL on the internet.
func publicBaseURL(dnsName string, port int) string {
	if port == 443 {
		return "https://" + dnsName
	}
	return "https://" + dnsName + ":" + strconv.Itoa(port)
}

type funnelSrcKey struct{}

// funnelConnContext records the internet client's address of a Funnel
// connection. It is the http.Server's ConnContext.
func funnelConnContext(ctx context.Context, c net.Conn) context.Context {
	if tc, ok := c.(*tls.Conn); ok {
		if fc, ok := tc.NetConn().(*ipn.FunnelConn); ok {
			return context.WithValue(ctx, funnelSrcKey{}, fc.Src)
		}
	}
	return ctx
}

// withFunnelSource makes the internet client's address the request's
// RemoteAddr. A request that did not arrive through Funnel is dropped:
// with FunnelOnly that cannot happen, and if it ever does, failing closed
// is the only safe answer.
func withFunnelSource(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		src, ok := r.Context().Value(funnelSrcKey{}).(netip.AddrPort)
		if !ok {
			logger.Error("dropped a public request that did not arrive through Funnel", "remoteAddr", r.RemoteAddr)
			panic(http.ErrAbortHandler)
		}
		r.RemoteAddr = src.String()
		next.ServeHTTP(w, r)
	})
}
