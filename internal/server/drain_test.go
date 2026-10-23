package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/hooks"
)

// A shutdown drains before it closes the tunnels: a call already delivered
// to the local app still gets its answer.
func TestDrainLetsDeliveredCallsFinish(t *testing.T) {
	e := newEnv(t, Config{})
	arrived := make(chan struct{})
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		time.Sleep(200 * time.Millisecond)
		io.WriteString(w, "done")
	}))
	defer local.Close()
	h, err := e.api.CreateHook(context.Background(), hooks.CreateRequest{Path: "slow"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.cli(h.ID, local.URL)()

	got := make(chan string, 1)
	go func() {
		resp := e.send(h, "", nil, nil)
		b, _ := io.ReadAll(resp.Body)
		got <- resp.Status + " " + string(b)
	}()
	<-arrived
	e.srv.Drain()
	if r := <-got; r != "200 OK done" {
		t.Fatalf("delivered call = %q", r)
	}
}
