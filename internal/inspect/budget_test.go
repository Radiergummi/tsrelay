package inspect

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
)

// The inspector's memory is bounded relay-wide, whatever senders send and
// however many hooks there are: past TotalBytes, the oldest calls of any
// hook go first.
func TestTotalBytesEvictsOldestCalls(t *testing.T) {
	const big = 10 << 10
	in := New(Config{TotalBytes: 100 << 10})
	header := http.Header{"X-Big": {strings.Repeat("a", big)}}
	for i := range 30 {
		hook := hooks.ID(fmt.Sprintf("h_%d", i%3))
		in.Add(Call{ID: fmt.Sprintf("c_%d", i), HookID: hook, RequestHeaders: header, Refused: i%5 == 0})
		in.Update(fmt.Sprintf("c_%d", i), func(c *Call) {
			c.Attempts = append(c.Attempts, Attempt{ResponseHeaders: header})
		})
		if got := in.Bytes(); got > 100<<10 {
			t.Fatalf("after call %d: %d bytes kept", i, got)
		}
	}
	if _, ok := in.Get("c_29"); !ok {
		t.Fatal("the newest call was evicted")
	}
	if _, ok := in.Get("c_0"); ok {
		t.Fatal("the oldest call is still kept")
	}
	kept := 0
	for i := range 3 {
		kept += len(in.List(hooks.ID(fmt.Sprintf("h_%d", i))))
	}
	if kept < 3 || kept > 5 {
		t.Fatalf("%d calls kept, want the 4 or so that fit", kept)
	}
	in.Forget("h_0")
	in.Forget("h_1")
	in.Forget("h_2")
	if got := in.Bytes(); got != 0 {
		t.Fatalf("%d bytes counted after forgetting every hook", got)
	}
}

// Calls a hook drops for its own limit must not stay reachable: the
// budget only bounds memory if nothing but the counted calls is kept.
func TestDroppedCallsAreReleased(t *testing.T) {
	in := New(Config{})
	for i := range 10000 { // many small calls keep the store busy
		in.Add(Call{ID: fmt.Sprintf("s_%d", i), HookID: hooks.ID(fmt.Sprintf("h_%d", i%200))})
	}
	for i := range 3000 { // one hook churns through ~96 MiB, keeping 100 calls
		header := http.Header{"X-Big": {strings.Repeat(string(rune('a'+i%26)), 32<<10)}} // a copy each
		in.Add(Call{ID: fmt.Sprintf("b_%d", i), HookID: "h_big", RequestHeaders: header})
	}
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.HeapAlloc > 64<<20 {
		t.Fatalf("heap holds %d MiB; the store counts %d MiB", ms.HeapAlloc>>20, in.Bytes()>>20)
	}
	runtime.KeepAlive(in)
}
