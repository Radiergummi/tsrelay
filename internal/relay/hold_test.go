package relay

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/identify"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/tunnel"
	"github.com/Radiergummi/tsrelay/internal/tunnel/tunneltest"
)

type callResult struct {
	status int
	code   string
	body   string
	took   time.Duration
}

// call sends a call in the background; its result arrives on the channel.
func (e *env) call(path, body string) chan callResult {
	c := make(chan callResult, 1)
	go func() {
		start := time.Now()
		req, _ := http.NewRequest("POST", e.srv.URL+path, strings.NewReader(body))
		resp, err := e.fake.Client(sender).Do(req)
		if err != nil {
			c <- callResult{code: err.Error(), took: time.Since(start)}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		c <- callResult{resp.StatusCode, resp.Header.Get(problem.HeaderError), string(b), time.Since(start)}
	}()
	return c
}

// waitHeld waits until n calls are held.
func (e *env) waitHeld(n int64) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for e.h.Held() != n {
		if time.Now().After(deadline) {
			e.t.Fatalf("held = %d, want %d", e.h.Held(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func echo() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(w, r.Body) })
}

func TestHoldUntilAttach(t *testing.T) {
	e := newEnv(t)
	got := e.call("/~moritz/cb", "held body")
	e.waitHeld(1)
	time.Sleep(50 * time.Millisecond) // so "waited" is clearly non-zero
	e.attach(echo(), tunnel.Options{})
	if res := <-got; res.status != 200 || res.body != "held body" {
		t.Fatalf("held call = %+v", res)
	}
	if e.h.Held() != 0 {
		t.Fatalf("held = %d after delivery", e.h.Held())
	}
	e.waitLog(`"msg":"call"`)
	if strings.Contains(e.log(), `"waited":0,`) {
		t.Fatalf("the call record does not show the wait:\n%s", e.log())
	}
}

// Attaching a tunnel for another hook must not deliver or wake a held call.
func TestHeldCallIgnoresOtherHooks(t *testing.T) {
	e := newEnv(t)
	other, err := e.mgr.Create(context.Background(), receiver, hooks.CreateRequest{Path: "other"})
	if err != nil {
		t.Fatal(err)
	}
	got := e.call("/~moritz/cb", "for cb")
	e.waitHeld(1)
	otherTun, _ := tunneltest.Pair(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the held call was delivered to another hook's tunnel")
	}), tunnel.Options{}, nil)
	e.reg.Attach(otherTun, []string{string(other.ID)})
	select {
	case res := <-got:
		t.Fatalf("held call ended on another hook's attach: %+v", res)
	case <-time.After(100 * time.Millisecond):
	}
	if e.h.Held() != 1 {
		t.Fatalf("held = %d, want 1", e.h.Held())
	}
	e.attach(echo(), tunnel.Options{})
	if res := <-got; res.status != 200 || res.body != "for cb" {
		t.Fatalf("held call = %+v", res)
	}
}

func TestHoldTimeout(t *testing.T) {
	e := newEnv(t)
	e.patch(hooks.SettingsPatch{HoldTimeout: hooks.Opt[hooks.Duration]{Set: true, Value: new(hooks.Duration(200 * time.Millisecond))}})
	resp := e.do(sender, "POST", "/~moritz/cb", nil, nil)
	wantProblem(t, resp, 561, problem.TunnelUnavailable)
	if ra := resp.Header.Get("Retry-After"); ra != "5" {
		t.Fatalf("Retry-After = %q, want at least 5", ra)
	}
}

func TestHoldRetryAfter(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "5", time.Second: "5", 7200 * time.Millisecond: "8", 30 * time.Second: "30"} {
		if got := holdRetryAfter(d); got != want {
			t.Errorf("holdRetryAfter(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestHoldQueueFull(t *testing.T) {
	e := newEnv(t) // HoldQueue: 2
	first, second := e.call("/~moritz/cb", "1"), e.call("/~moritz/cb", "2")
	e.waitHeld(2)
	start := time.Now()
	resp := e.do(sender, "POST", "/~moritz/cb", nil, nil)
	wantProblem(t, resp, 561, problem.TunnelUnavailable)
	if time.Since(start) > 2*time.Second || resp.Header.Get("Retry-After") != "30" {
		t.Fatalf("a full queue must fail at once with Retry-After 30; took %v, Retry-After %q", time.Since(start), resp.Header.Get("Retry-After"))
	}
	e.attach(echo(), tunnel.Options{})
	for _, c := range []chan callResult{first, second} {
		if res := <-c; res.status != 200 {
			t.Fatalf("held call = %+v", res)
		}
	}
}

// A sender that gives up while its call is held gets no answer, and the
// call is not recorded as tunnel_unavailable: no tunnel failed it.
func TestHeldCallCanceledBySender(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", e.srv.URL+"/~moritz/cb", nil)
	done := make(chan error, 1)
	go func() {
		resp, err := e.fake.Client(sender).Do(req)
		if err == nil {
			resp.Body.Close()
		}
		done <- err
	}()
	e.waitHeld(1)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("the canceled call got a response")
	}
	e.waitLog(`"msg":"call"`)
	if strings.Contains(e.log(), problem.TunnelUnavailable) {
		t.Fatalf("a call the sender canceled was recorded as %s:\n%s", problem.TunnelUnavailable, e.log())
	}
}

// A hook deleted while calls wait for it releases them at once with 404.
func TestHeldCallsReleasedOnDelete(t *testing.T) {
	e := newEnv(t)
	got := e.call("/~moritz/cb", "")
	// Held counts the call before its Wait registers, so Release may run
	// first; Wait's existence re-check fails the call all the same.
	e.waitHeld(1)
	if err := e.mgr.Delete(context.Background(), e.hook.ID); err != nil {
		t.Fatal(err)
	}
	e.reg.Release(string(e.hook.ID)) // what DELETE /hooks/{id} and the reaper do
	res := <-got
	if res.status != 404 || res.code != problem.HookNotFound || res.took > 5*time.Second {
		t.Fatalf("held call after delete = %+v", res)
	}
}

// A hook that expires while calls wait for it releases them at its expiry,
// not after holdTimeout.
func TestHeldCallsReleasedOnExpiry(t *testing.T) {
	e := newEnv(t)
	exp := time.Now().Add(300 * time.Millisecond)
	if _, err := e.mgr.Create(context.Background(), receiver, hooks.CreateRequest{Path: "short", ExpiresAt: &exp}); err != nil {
		t.Fatal(err)
	}
	res := <-e.call("/~moritz/short", "")
	if res.status != 404 || res.code != problem.HookNotFound {
		t.Fatalf("held call on expiry = %+v", res)
	}
	// Measured against the expiry itself: creating the hook and starting the
	// call can take a while under -race.
	if late := time.Since(exp); late < -50*time.Millisecond || late > 5*time.Second {
		t.Fatalf("released %v after the expiry; want at the expiry, not at the 30s hold timeout", late)
	}
}

// A sender that gives up while its call is held is recorded as
// sender_canceled, not as ok.
func TestSenderCanceledWhileHeld(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", e.srv.URL+"/~moritz/cb", nil)
	time.AfterFunc(100*time.Millisecond, cancel)
	if resp, err := e.fake.Client(sender).Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("the canceled request succeeded")
	}
	e.waitLog(`"error":"sender_canceled"`)
}

// A held call is authorized again once a tunnel attaches: a call to a
// rotated public URL, or from a sender the hook's new channel shuts out,
// gets the 404 it would get if it arrived now, and reaches nobody.
func TestHeldCallReauthorized(t *testing.T) {
	refused := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the held call was delivered")
	})
	t.Run("rotated public URL", func(t *testing.T) {
		e := newPublicEnv(t)
		got := make(chan *http.Response, 1)
		go func() {
			resp, err := http.Post(e.pub.URL+e.hook.PublicRoute()+"/x", "application/json", strings.NewReader("{}"))
			if err != nil {
				t.Error(err)
			}
			got <- resp
		}()
		e.waitHeld(1)
		if _, err := e.mgr.RotatePublicToken(context.Background(), publicReceiver, e.hook.ID); err != nil {
			t.Fatal(err)
		}
		e.attachPublic(refused)
		resp := <-got
		defer resp.Body.Close()
		wantProblem(t, resp, 404, problem.HookNotFound)
	})
	t.Run("channel changed", func(t *testing.T) {
		e := newEnv(t)
		channel := func(c string) {
			if _, _, err := e.mgr.Update(context.Background(), receiver, e.hook.ID,
				hooks.UpdateRequest{Channel: hooks.Opt[string]{Set: true, Value: new(c)}}); err != nil {
				t.Fatal(err)
			}
		}
		channel("staging")
		staging := identify.Tagged("w2", []string{"tag:staging"}, `{"roles":["sender"],"channels":["staging"]}`)
		got := make(chan *http.Response, 1)
		go func() {
			resp, err := e.fake.Client(staging).Post(e.srv.URL+"/~moritz/cb", "text/plain", strings.NewReader("x"))
			if err != nil {
				t.Error(err)
			}
			got <- resp
		}()
		e.waitHeld(1)
		channel("prod")
		e.attach(refused, tunnel.Options{})
		resp := <-got
		defer resp.Body.Close()
		wantProblem(t, resp, 404, problem.HookNotFound)
	})
}
