package tunnel

import (
	"cmp"
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/Radiergummi/tsrelay/internal/ids"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// ErrNoHooks means every hook of the client was taken over by another
// tunnel or deleted, so there is nothing left to reconnect for.
var ErrNoHooks = errors.New("tunnel: no hooks left to serve (taken over by another tunnel, revoked, or deleted)")

var errTunnelLost = errors.New("tunnel lost")

// Client keeps a tunnel attached, redialing with exponential backoff and
// full jitter. Hooks the relay reports as replaced, revoked or deleted are
// dropped from later attach attempts.
type Client struct {
	Hooks []string
	// Target is the local target reported to the relay for display.
	Target     string
	Dial       func(ctx context.Context, hooks []string, a Attach) (net.Conn, error)
	Handler    http.Handler
	OnControl  func(Control)
	OnState    func(connected bool, err error)
	MinBackoff time.Duration
	MaxBackoff time.Duration

	mu    sync.Mutex
	hooks []string
}

// Run returns nil when ctx is done, ErrNoHooks when no hooks remain, or the
// relay's 4xx refusal, which retrying would not fix.
func (c *Client) Run(ctx context.Context) error {
	c.mu.Lock()
	c.hooks = slices.Clone(c.Hooks)
	c.mu.Unlock()
	minB := cmp.Or(c.MinBackoff, 250*time.Millisecond)
	maxB := cmp.Or(c.MaxBackoff, 30*time.Second)
	backoff := minB
	attach := Attach{Session: ids.New("s_"), Target: c.Target}
	for {
		hooks := c.Current()
		if len(hooks) == 0 {
			return ErrNoHooks
		}
		conn, err := c.Dial(ctx, hooks, attach)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if len(c.Current()) == 0 { // Dial removed the last hook
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
		Serve(ctx, conn, c.Handler, c.handleControl)
		if ctx.Err() != nil {
			return nil
		}
		if len(c.Current()) == 0 {
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

// Current returns the hooks the next dial attaches.
func (c *Client) Current() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.hooks)
}

// Replace swaps oldID for newID in the hooks later dials attach, e.g. after
// the caller recreated a hook the relay had deleted. Unknown IDs are ignored.
func (c *Client) Replace(oldID, newID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i := slices.Index(c.hooks, oldID); i >= 0 {
		c.hooks[i] = newID
	}
}

// Remove drops id from the hooks later dials attach, e.g. after the relay
// refused a redial for it. Unknown IDs are ignored.
func (c *Client) Remove(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hooks = slices.DeleteFunc(c.hooks, func(x string) bool { return x == id })
}

func (c *Client) handleControl(ctl Control) {
	if ctl.Type == ControlReplaced || ctl.Type == ControlRevoked || ctl.Type == ControlHookDeleted {
		for _, h := range ctl.Hooks {
			c.Remove(h.ID)
		}
	}
	if c.OnControl != nil {
		c.OnControl(ctl)
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
