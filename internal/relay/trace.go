package relay

import (
	"net/http"
	"time"
)

// forwardTransport sends a call down its tunnel and notes when the app's
// response headers arrived, which ends the call's time to first byte.
// The reverse proxy calls it on the handler's goroutine, so headers needs
// no lock.
type forwardTransport struct {
	rt      http.RoundTripper
	headers *time.Time
}

func (t forwardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.rt.RoundTrip(req)
	if err == nil {
		*t.headers = time.Now()
	}
	return resp, err
}
