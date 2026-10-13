package inspect

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"tailscale.com/tailcfg"
)

func TestBuffer(t *testing.T) {
	b := newBuffer(4, true)
	b.Write([]byte("hello "))
	b.Write([]byte("world"))
	if c := b.Capture(true); string(c.Body) != "hell" || c.Size != 11 || !c.Truncated || !c.Captured || c.Complete() {
		t.Fatalf("over the limit: %+v", c)
	}
	b = newBuffer(64, true)
	b.Write([]byte("hi"))
	if c := b.Capture(true); string(c.Body) != "hi" || c.Truncated || !c.Complete() {
		t.Fatalf("under the limit: %+v", c)
	}
	if c := b.Capture(false); !c.Truncated || c.Complete() {
		t.Fatalf("a body that was not read in full must count as truncated: %+v", c)
	}
	b = newBuffer(64, false)
	b.Write([]byte("secret"))
	if c := b.Capture(true); c.Body != nil || c.Size != 6 || c.Captured || c.Complete() {
		t.Fatalf("capture off: %+v", c)
	}
	if c := newBuffer(64, false).Capture(true); !c.Complete() {
		t.Fatalf("an empty body is complete even without capture: %+v", c)
	}
	var nilBuf *Buffer
	if n, err := nilBuf.Write([]byte("x")); n != 1 || err != nil {
		t.Fatal("a nil Buffer must accept writes")
	}
}

func TestRedactor(t *testing.T) {
	r := NewRedactor([]string{"Authorization", "set-cookie", " x-api-key "})
	kept, redacted := r.Headers(http.Header{
		"authorization": {"Bearer s3cr3t"}, // not canonical, as a hand-built header map may be
		"Set-Cookie":    {"a=1", "b=2"},
		"X-Api-Key":     {"k"},
		"Content-Type":  {"application/json"},
	})
	if len(kept) != 1 || kept.Get("Content-Type") != "application/json" {
		t.Fatalf("kept = %v", kept)
	}
	if !slices.Equal(redacted, []string{"Authorization", "Set-Cookie", "X-Api-Key"}) {
		t.Fatalf("redacted = %v", redacted)
	}
	for _, v := range kept {
		for _, s := range v {
			if s == "Bearer s3cr3t" || s == "k" {
				t.Fatal("a redacted value was kept")
			}
		}
	}
}

func TestRedactorUnannouncedTrailers(t *testing.T) {
	r := NewRedactor([]string{"Authorization"})
	kept, redacted := r.Headers(http.Header{
		http.TrailerPrefix + "Authorization": {"Bearer s3cr3t"}, // an unannounced trailer, stashed by ReverseProxy
		http.TrailerPrefix + "X-Custom":      {"kept"},
	})
	if !slices.Equal(redacted, []string{"Authorization"}) {
		t.Fatalf("redacted = %v, want the canonical name without the Trailer: prefix", redacted)
	}
	if v := kept[http.TrailerPrefix+"X-Custom"]; len(v) != 1 || v[0] != "kept" {
		t.Fatalf("kept = %v", kept)
	}
	if _, ok := kept[http.TrailerPrefix+"Authorization"]; ok {
		t.Fatal("an unannounced trailer escaped redaction")
	}
}

func TestRedactCapsHeaders(t *testing.T) {
	in := New(Config{CaptureLimit: 10})
	kept, redacted := in.Redact(http.Header{"A": {"12345"}, "B": {"12345"}, "C": {"12345"}})
	if len(kept) != 1 || kept.Get("A") != "12345" {
		t.Fatalf("over the limit: kept = %v, want only the prefix in sorted-name order", kept)
	}
	if !slices.Equal(redacted, []string{"B", "C"}) {
		t.Fatalf("over the limit: redacted = %v", redacted)
	}

	in = New(Config{})
	kept, redacted = in.Redact(http.Header{"X-Custom": {"small"}})
	if len(kept) != 1 || kept.Get("X-Custom") != "small" || len(redacted) != 0 {
		t.Fatalf("under the limit: kept = %v, redacted = %v, want it unchanged", kept, redacted)
	}
}

func TestTruncateQuery(t *testing.T) {
	in := New(Config{CaptureLimit: 4})
	if got := in.TruncateQuery("abcdef"); got != "abcd" {
		t.Fatalf("over the limit: %q", got)
	}
	if got := in.TruncateQuery("ab"); got != "ab" {
		t.Fatalf("under the limit: %q", got)
	}
}

func call(id string, hook hooks.ID) Call {
	return Call{ID: id, HookID: hook, Owner: 1, Kind: KindSender, Method: "POST", Path: "/x", Outcome: OutcomePending}
}

func TestStoreRetention(t *testing.T) {
	in := New(Config{CallsPerHook: 3})
	for i := 1; i <= 4; i++ {
		in.Add(call(fmt.Sprintf("c%d", i), "h_a"))
	}
	in.Add(call("b1", "h_b"))
	var ids []string
	for _, s := range in.List("h_a") {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"c4", "c3", "c2"}) {
		t.Fatalf("list = %v, want newest first without the evicted c1", ids)
	}
	if _, ok := in.Get("c1"); ok {
		t.Fatal("an evicted call is still found by ID")
	}
	in.Forget("h_a")
	if len(in.List("h_a")) != 0 {
		t.Fatal("Forget left calls in the list")
	}
	if _, ok := in.Get("c3"); ok {
		t.Fatal("Forget left calls findable by ID")
	}
	if _, ok := in.Get("b1"); !ok {
		t.Fatal("Forget removed another hook's call")
	}
}

func TestStoreUpdateAndOnChange(t *testing.T) {
	in := New(Config{})
	var seen []Summary
	in.OnChange = func(owner tailcfg.UserID, s Summary) {
		if owner != 1 {
			t.Errorf("owner = %d", owner)
		}
		seen = append(seen, s)
	}
	in.Add(call("c1", "h_a"))
	in.Update("c1", func(c *Call) { c.Outcome, c.Status = OutcomeOK, 200 })
	in.Update("gone", func(*Call) { t.Error("Update ran for an unknown call") })
	if len(seen) != 2 || seen[0].Outcome != OutcomePending || seen[1].Outcome != OutcomeOK || seen[1].Status != 200 {
		t.Fatalf("changes = %+v", seen)
	}
	got, _ := in.Get("c1")
	got.Method = "MUTATED"
	if again, _ := in.Get("c1"); again.Method != "POST" {
		t.Fatal("Get must return a copy")
	}
}

func TestMetadata(t *testing.T) {
	c := Call{
		ID: "c1", Method: "POST", Path: "/x", Query: "token=s3cr3t",
		RequestHeaders: http.Header{"X-A": {"1"}}, Redacted: []string{"Authorization"},
		RequestBody: Capture{Body: []byte("{}"), Size: 2, Captured: true},
		Attempts:    []Attempt{{Status: 200, ResponseHeaders: http.Header{"X-B": {"2"}}, ResponseBody: Capture{Body: []byte("ok"), Size: 2, Captured: true}}},
	}
	m := c.Metadata()
	if m.Query != "" || m.RequestHeaders != nil || m.Redacted != nil || m.RequestBody.Body != nil ||
		m.Attempts[0].ResponseHeaders != nil || m.Attempts[0].ResponseBody.Body != nil {
		t.Fatalf("metadata kept owner-only fields: %+v", m)
	}
	if m.RequestBody.Size != 2 || m.Attempts[0].Status != 200 || m.Path != "/x" {
		t.Fatalf("metadata lost metadata: %+v", m)
	}
	if c.Attempts[0].ResponseBody.Body == nil {
		t.Fatal("Metadata modified the original call")
	}
}
