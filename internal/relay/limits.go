package relay

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"golang.org/x/time/rate"
)

// Causes with which a call's context is cancelled when it breaks a limit.
var (
	errIdleTimeout  = errors.New("no body traffic within the idle timeout")
	errBodyTooLarge = errors.New("request body larger than the hook's maxBodyBytes")
)

// idleWatch fires when neither the request body nor the response body moved
// for timeout. It runs while the request body streams and again once the
// response has started. In between, responseTimeout governs instead.
type idleWatch struct {
	timeout time.Duration
	timer   *time.Timer

	mu         sync.Mutex
	responding bool
	stopped    bool
}

func newIdleWatch(timeout time.Duration, fire func()) *idleWatch {
	w := &idleWatch{timeout: timeout}
	w.timer = time.AfterFunc(timeout, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if !w.stopped {
			fire()
		}
	})
	w.timer.Stop() // armed by the first body traffic
	return w
}

// touch records body traffic and restarts the countdown.
func (w *idleWatch) touch() { w.timer.Reset(w.timeout) }

// respond marks the start of the response; from now on the watch runs
// until the call ends.
func (w *idleWatch) respond() {
	w.mu.Lock()
	w.responding = true
	w.mu.Unlock()
	w.touch()
}

// requestDone pauses the watch when the request body is complete and the
// response has not started yet.
func (w *idleWatch) requestDone() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.responding {
		w.timer.Stop()
	}
}

// stop ends the watch. A countdown that expires concurrently does nothing.
func (w *idleWatch) stop() {
	w.mu.Lock()
	w.stopped = true
	w.mu.Unlock()
	w.timer.Stop()
}

// callBody is the request body on its way to the tunnel. It enforces the
// hook's maxBodyBytes (0: unlimited) and feeds the idle watch.
type callBody struct {
	io.ReadCloser
	max    int64
	read   int64
	idle   *idleWatch
	cancel context.CancelCauseFunc
}

func (b *callBody) Read(p []byte) (int, error) {
	b.idle.touch()
	if b.max > 0 {
		if b.read > b.max {
			return 0, errBodyTooLarge
		}
		if rest := b.max - b.read + 1; int64(len(p)) > rest {
			p = p[:rest] // one byte more than allowed reveals an oversized body
		}
	}
	n, err := b.ReadCloser.Read(p)
	b.read += int64(n)
	if b.max > 0 && b.read > b.max {
		b.cancel(errBodyTooLarge)
		return n - int(b.read-b.max), errBodyTooLarge
	}
	if err == io.EOF {
		b.idle.requestDone()
	} else {
		b.idle.touch()
	}
	return n, err
}

// idleWriter feeds the idle watch from the response side.
type idleWriter struct {
	http.ResponseWriter
	idle *idleWatch
}

func (w *idleWriter) WriteHeader(code int) {
	if code >= http.StatusOK {
		w.idle.respond()
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *idleWriter) Write(b []byte) (int, error) {
	w.idle.touch()
	n, err := w.ResponseWriter.Write(b)
	w.idle.touch()
	return n, err
}

func (w *idleWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// limiters holds a token bucket per hook. A bucket is rebuilt, full, when
// its hook's rate limit changes, so an update applies from the next call.
type limiters struct {
	mu     sync.Mutex
	byHook map[hooks.ID]*hookLimiter
	swept  time.Time
}

type hookLimiter struct {
	cfg hooks.RateLimit
	l   *rate.Limiter
}

// sweepInterval is how often limiters of deleted hooks are dropped.
const sweepInterval = time.Minute

// allow takes a token from hook id's bucket. If none is left, it returns
// how long until one will be. exists reports whether a hook still exists;
// it is used to drop the buckets of deleted hooks now and then.
func (ls *limiters) allow(id hooks.ID, rl hooks.RateLimit, now time.Time, exists func(hooks.ID) bool) (time.Duration, bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.byHook == nil {
		ls.byHook = map[hooks.ID]*hookLimiter{}
		ls.swept = now
	}
	if now.Sub(ls.swept) >= sweepInterval {
		for hid := range ls.byHook {
			if hid != id && !exists(hid) {
				delete(ls.byHook, hid)
			}
		}
		ls.swept = now
	}
	hl := ls.byHook[id]
	if hl == nil || hl.cfg != rl {
		hl = &hookLimiter{cfg: rl, l: rate.NewLimiter(rate.Limit(rl.PerSecond), rl.Burst)}
		ls.byHook[id] = hl
	}
	r := hl.l.ReserveN(now, 1)
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return d, false
	}
	return 0, true
}

// retryAfter renders d as whole seconds for Retry-After, at least 1.
func retryAfter(d time.Duration) string {
	return strconv.Itoa(max(1, int(math.Ceil(d.Seconds()))))
}
