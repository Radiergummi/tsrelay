package inspect

import (
	"net/http"
	"testing"
)

func BenchmarkRedact(b *testing.B) {
	r := NewRedactor(DefaultRedact)
	h := http.Header{
		"Authorization": {"Bearer x"}, "Content-Type": {"application/json"}, "User-Agent": {"Stripe/1.0"},
		"Stripe-Signature": {"t=1,v1=abc"}, "Cookie": {"a=b"}, "Accept-Encoding": {"gzip"}, "X-Request-Id": {"r1"},
	}
	b.ReportAllocs()
	for b.Loop() {
		r.Headers(h)
	}
}

// BenchmarkCapture writes a 1 MiB body in 32 KiB reads into a capture
// buffer, as the relay does while streaming a call.
func BenchmarkCapture(b *testing.B) {
	chunk := make([]byte, 32<<10)
	for _, keep := range []bool{true, false} {
		name := "bodies"
		if !keep {
			name = "noBodies"
		}
		b.Run(name, func(b *testing.B) {
			in := New(Config{})
			b.SetBytes(1 << 20)
			b.ReportAllocs()
			for b.Loop() {
				buf := in.NewBuffer(keep)
				for range 32 {
					buf.Write(chunk)
				}
				buf.Capture(true)
			}
		})
	}
}
