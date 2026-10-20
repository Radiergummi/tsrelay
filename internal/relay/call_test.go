package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/problem"
)

// testCall is a call that answered 200, with parent as the sender's context.
func testCall(parent context.Context) *call {
	return &call{parent: parent, ctx: parent, start: time.Now(),
		rec:  &recorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK},
		body: &countingReader{ReadCloser: http.NoBody},
		r:    httptest.NewRequest("POST", "/~m/cb", nil)}
}

// A call no step ended is ok, even if its sender is gone by the end: the
// response went out in full.
func TestUnrecordedOutcomeIsOK(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	if res := testCall(parent).result(); res.Code != "" || res.Outcome() != "ok" {
		t.Fatalf("outcome = %q, want ok", res.Outcome())
	}
}

// The first outcome a step records stands: the forwarder's answer that the
// app is unreachable is not overwritten by the abort that follows it.
func TestFirstOutcomeWins(t *testing.T) {
	c := testCall(context.Background())
	c.setOutcome(problem.UpstreamUnreachable)
	c.aborted = true
	c.setOutcome(c.abortCause())
	if got := c.result().Code; got != problem.UpstreamUnreachable {
		t.Fatalf("code = %q, want %q", got, problem.UpstreamUnreachable)
	}
}
