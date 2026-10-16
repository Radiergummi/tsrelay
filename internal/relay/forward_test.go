package relay

import (
	"io"
	"strings"
	"testing"
)

// A RoundTripper may go on reading a request body after it returns
// (http.RoundTripper), so the handler reads the counts while the
// transport may still be reading. The race detector checks this.
func TestCountingReaderCountsConcurrently(t *testing.T) {
	c := &countingReader{ReadCloser: io.NopCloser(strings.NewReader(strings.Repeat("x", 1<<16)))}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 16)
		for {
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-done:
			if n, eof := c.counts(); n != 1<<16 || !eof {
				t.Fatalf("counts = %d, %v; want %d, true", n, eof, 1<<16)
			}
			return
		default:
			c.counts()
		}
	}
}
