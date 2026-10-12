package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
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
