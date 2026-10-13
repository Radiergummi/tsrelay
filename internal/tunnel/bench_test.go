package tunnel_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/Radiergummi/tsrelay/internal/tunnel/tunneltest"
)

// BenchmarkRoundTrip measures one request and response over a tunnel pair,
// without the relay around it: the tunnel's framing cost.
func BenchmarkRoundTrip(b *testing.B) {
	tun, _ := tunneltest.Pair(b, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), tunnel.Options{}, nil)
	b.ReportAllocs()
	for b.Loop() {
		req, _ := http.NewRequest("GET", "http://tunnel/x", nil)
		resp, err := tun.RoundTrip(req)
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}
