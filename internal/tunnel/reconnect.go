package tunnel

import (
	"cmp"
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"net/http"
	"time"

	"github.com/Radiergummi/tsrelay/internal/ids"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// ErrNoHooks means every hook of the client was taken over by another
// tunnel or deleted, so there is nothing left to reconnect for.
var ErrNoHooks = errors.New("tunnel: no hooks left to serve (taken over by another tunnel, revoked, or deleted)")

var errTunnelLost = errors.New("tunnel lost")

// Client keeps a tunnel attached, redialing with exponential backoff and
// full jitter.
type Client struct {
	// List is the hooks to dial, owned by the caller: it is asked before
	// every dial, and the caller follows the control messages that take
	// hooks away (OnControl). Run ends once it is empty.
	List func() []string
	// Target is the local target reported to the relay for display.
	Target     string
	Dial       func(ctx context.Context, hooks []string, a Attach) (net.Conn, error)
	Handler    http.Handler
	OnControl  func(Control)
	OnState    func(connected bool, err error)
	MinBackoff time.Duration
	MaxBackoff time.Duration
}

// Run returns nil when ctx is done, ErrNoHooks when no hooks remain, or the
// relay's 4xx refusal, which retrying would not fix.
func (c *Client) Run(ctx context.Context) error {
	minB := cmp.Or(c.MinBackoff, 250*time.Millisecond)
	maxB := cmp.Or(c.MaxBackoff, 30*time.Second)
	backoff := minB
	attach := Attach{Session: ids.New("s_"), Target: c.Target}
	for {
		hooks := c.List()
		if len(hooks) == 0 {
			return ErrNoHooks
		}
		conn, err := c.Dial(ctx, hooks, attach)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if len(c.List()) == 0 { // Dial removed the last hook
				return ErrNoHooks
			}
			var p *problem.Problem
			if errors.As(err, &p) && p.Status >= 400 && p.Status < 500 {
				return err
			}
			c.state(false, err)
			if !sleep(ctx, rand.N(backoff)+1) {
				return nil
			}
			backoff = min(backoff*2, maxB)
			continue
		}
		attach.Resume = true
		c.state(true, nil)
		served := time.Now()
		Serve(ctx, conn, c.Handler, c.OnControl)
		if ctx.Err() != nil {
			return nil
		}
		if len(c.List()) == 0 {
			return ErrNoHooks
		}
		c.state(false, errTunnelLost)
		// A tunnel that held for a while redials promptly; one lost right
		// after attaching backs off like a failed dial, so a relay that
		// accepts and then drops does not get a tight redial loop.
		if time.Since(served) >= maxB {
			backoff = minB
		}
		if !sleep(ctx, rand.N(backoff)+1) {
			return nil
		}
		backoff = min(backoff*2, maxB)
	}
}

func (c *Client) state(connected bool, err error) {
	if c.OnState != nil {
		c.OnState(connected, err)
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
