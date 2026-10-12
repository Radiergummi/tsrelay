package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var errResponseTimeout = errors.New("no response headers within the response timeout")

// timeoutTransport bounds the wait for response headers without limiting how
// long a streamed body may then take.
type timeoutTransport struct {
	rt      http.RoundTripper
	timeout time.Duration
}

func (t timeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(req.Context())
	timer := time.AfterFunc(t.timeout, func() { cancel(errResponseTimeout) })
	resp, err := t.rt.RoundTrip(req.WithContext(ctx))
	if !timer.Stop() && err == nil {
		// The timeout fired as the headers arrived. The context is
		// cancelled, so the body would fail; report the timeout instead.
		resp.Body.Close()
		err = errResponseTimeout
	}
	if err != nil {
		if context.Cause(ctx) == errResponseTimeout {
			err = errResponseTimeout
		}
		cancel(nil)
		return nil, err
	}
	resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: func() { cancel(nil) }}
	return resp, nil
}

type cancelBody struct {
	io.ReadCloser
	once   sync.Once
	cancel func()
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.cancel)
	return err
}

// splitRoute splits "/~slug/rest" into slug and rest.
func splitRoute(path string) (slug, rest string, ok bool) {
	after, found := strings.CutPrefix(path, "/~")
	if !found {
		return "", "", false
	}
	slug, rest, _ = strings.Cut(after, "/")
	return slug, rest, slug != "" && rest != ""
}

// remainder returns u's path after its first n segments, decoded and
// escaped, always starting with "/". The escaped form is what gets
// forwarded, so encodings such as %2F survive the relay.
func remainder(u *url.URL, n int) (path, rawPath string) {
	rest := u.EscapedPath()
	for range n {
		i := strings.IndexByte(rest[1:], '/')
		if i < 0 {
			rest = "/"
			break
		}
		rest = rest[1+i:]
	}
	decoded, err := url.PathUnescape(rest)
	if err != nil || decoded == rest {
		return rest, ""
	}
	return decoded, rest
}

// encodedSlashIn reports whether the first n segments of u's escaped path
// contain an encoded slash.
func encodedSlashIn(u *url.URL, n int) bool {
	segs := strings.SplitN(strings.TrimPrefix(u.EscapedPath(), "/"), "/", n+1)
	for _, s := range segs[:min(n, len(segs))] {
		if strings.Contains(strings.ToLower(s), "%2f") {
			return true
		}
	}
	return false
}

// recorder captures the status and size of a response for the audit log. It
// unwraps so http.ResponseController can still flush streamed responses.
type recorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 && code >= 200 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

type countingReader struct {
	io.ReadCloser
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.n += int64(n)
	return n, err
}
