package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/http2"
)

// ErrOverloaded is returned by RoundTrip when the tunnel's concurrency limit
// is reached. It never blocks waiting for a slot.
var ErrOverloaded = errors.New("tunnel: concurrency limit reached")

type Options struct {
	Concurrency int // concurrent forwarded requests; default DefaultConcurrency
}

// IsUpgrade reports whether r asks to open a tunnel.
func IsUpgrade(r *http.Request) bool {
	return httpguts.HeaderValuesContainsToken(r.Header["Connection"], "upgrade") &&
		r.Header.Get("Upgrade") == UpgradeToken
}

// Accept answers an upgrade request (check IsUpgrade first) with 101 and
// takes over the connection.
func Accept(w http.ResponseWriter, r *http.Request) (net.Conn, error) {
	conn, brw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{}) // clear deadlines the HTTP server may have set
	if _, err := io.WriteString(brw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: "+UpgradeToken+"\r\n\r\n"); err != nil {
		conn.Close()
		return nil, err
	}
	if err := brw.Flush(); err != nil {
		conn.Close()
		return nil, err
	}
	return &bufferedConn{Conn: conn, r: brw.Reader}, nil
}

// Tunnel is the relay's end of an attached tunnel.
type Tunnel struct {
	ID    string
	Owner authz.Identity
	// Session identifies the client process that opened the tunnel.
	Session string
	// RemoteAddr is the client's address as the relay saw it when the
	// tunnel attached. The revalidator identifies it again.
	RemoteAddr string

	cc   *http2.ClientConn
	conn *watchedConn
	sem  chan struct{}
}

// NewTunnel starts speaking HTTP/2, as client, on an accepted connection.
func NewTunnel(conn net.Conn, id string, owner authz.Identity, opts Options) (*Tunnel, error) {
	if opts.Concurrency <= 0 {
		opts.Concurrency = DefaultConcurrency
	}
	wc := &watchedConn{Conn: conn, done: make(chan struct{})}
	tr := &http2.Transport{
		AllowHTTP:          true,
		DisableCompression: true, // bodies must pass through byte for byte
		ReadIdleTimeout:    pingInterval,
		PingTimeout:        pingInterval,
	}
	cc, err := tr.NewClientConn(wc)
	if err != nil {
		wc.Close()
		return nil, err
	}
	return &Tunnel{ID: id, Owner: owner, cc: cc, conn: wc, sem: make(chan struct{}, opts.Concurrency)}, nil
}

// Done is closed once the tunnel's connection has failed or been closed.
func (t *Tunnel) Done() <-chan struct{} { return t.conn.done }

func (t *Tunnel) Close() error {
	t.cc.Close()
	return t.conn.Close()
}

// RoundTrip forwards req down the tunnel. It fails immediately with
// ErrOverloaded when the concurrency limit is reached; the slot is released
// when the response body is closed.
func (t *Tunnel) RoundTrip(req *http.Request) (*http.Response, error) {
	select {
	case t.sem <- struct{}{}:
	default:
		return nil, ErrOverloaded
	}
	resp, err := t.cc.RoundTrip(req)
	if err != nil {
		<-t.sem
		return nil, err
	}
	resp.Body = &releaseBody{ReadCloser: resp.Body, release: func() { <-t.sem }}
	return resp, nil
}

type releaseBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *releaseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

// Send delivers a control message. It does not count against the
// concurrency limit.
func (t *Tunnel) Send(ctx context.Context, c Control) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://tunnel/", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set(HeaderControl, "1")
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.cc.RoundTrip(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("control %s: client answered %d", c.Type, resp.StatusCode)
	}
	return nil
}

// Notify sends c in the background and then closes the tunnel if
// closeAfter is set, e.g. because it no longer serves any hooks.
func (t *Tunnel) Notify(c Control, closeAfter bool) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), ControlTimeout)
		defer cancel()
		_ = t.Send(ctx, c)
		if closeAfter {
			t.Close()
		}
	}()
}
