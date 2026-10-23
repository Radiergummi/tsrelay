package inspect

import (
	"cmp"
	"net/http"
	"slices"
	"sync"

	"github.com/Radiergummi/tsrelay/internal/hooks"
	"tailscale.com/tailcfg"
)

const (
	DefaultCallsPerHook = 100
	DefaultCaptureLimit = 64 << 10
	DefaultTotalBytes   = 128 << 20

	// callOverhead is what a call takes beyond the bytes it captured, as
	// counted against TotalBytes: IDs, times, the sender and so on.
	callOverhead = 512

	// refusedPerHook is how many refused calls are kept per hook, apart
	// from the others, so a sender shut out by the channel cannot evict
	// delivered calls (DESIGN.md §9.2).
	refusedPerHook = 10
)

// Config configures the inspector (DESIGN.md §9.2, §12).
type Config struct {
	CallsPerHook int      // calls kept per hook; 0 means DefaultCallsPerHook
	CaptureLimit int      // bytes kept per body; 0 means DefaultCaptureLimit
	NoBodies     bool     // turns body capture off relay-wide (--capture-bodies=false)
	TotalBytes   int64    // bytes kept of all calls together; 0 means DefaultTotalBytes
	Redact       []string // headers never captured, in addition to DefaultRedact
}

// Inspector keeps the most recent calls of each hook in memory. Calls are
// lost on restart (DESIGN.md §11).
type Inspector struct {
	cfg    Config
	redact Redactor
	// OnChange, if set, is called after a call was added or changed,
	// except for refused calls, which are not announced. It runs with the store's lock held, so changes are reported in order; it
	// must not block or call the Inspector. Set it before first use.
	OnChange func(owner tailcfg.UserID, channel string, s Summary)

	mu      sync.Mutex
	byHook  map[hooks.ID][]*Call // oldest first
	refused map[hooks.ID][]*Call // oldest first; the calls with Refused set
	byID    map[string]*Call
	// order has every kept call, oldest first, and calls dropped since,
	// which evictLocked skips. sizes and total count what each kept call
	// and all of them take, so TotalBytes bounds the inspector's memory.
	order []*Call
	sizes map[string]int64
	total int64
}

func New(cfg Config) *Inspector {
	cfg.CallsPerHook = cmp.Or(cfg.CallsPerHook, DefaultCallsPerHook)
	cfg.CaptureLimit = cmp.Or(cfg.CaptureLimit, DefaultCaptureLimit)
	cfg.TotalBytes = cmp.Or(cfg.TotalBytes, DefaultTotalBytes)
	return &Inspector{cfg: cfg, redact: NewRedactor(append(slices.Clone(DefaultRedact), cfg.Redact...)),
		byHook: map[hooks.ID][]*Call{}, refused: map[hooks.ID][]*Call{}, byID: map[string]*Call{}, sizes: map[string]int64{}}
}

// CallsPerHook is how many calls the inspector keeps per hook.
func (in *Inspector) CallsPerHook() int { return in.cfg.CallsPerHook }

// NoBodies reports whether body capture is off relay-wide.
func (in *Inspector) NoBodies() bool { return in.cfg.NoBodies }

// Redact returns h without the redacted headers and those left out to keep
// the set within the capture limit, and the sorted names of every header it
// left out, for either reason.
func (in *Inspector) Redact(h http.Header) (http.Header, []string) {
	kept, redacted := in.redact.Headers(h)
	return in.capHeaders(kept, redacted)
}

// capHeaders keeps headers from h, visited in sorted name order so the
// result is deterministic, while the running total of name and value bytes
// stays within the capture limit. Headers left out for size are appended to
// redacted.
func (in *Inspector) capHeaders(h http.Header, redacted []string) (http.Header, []string) {
	total := 0
	names := make([]string, 0, len(h))
	for k, vs := range h {
		names = append(names, k)
		total += headerSize(k, vs)
	}
	if total <= in.cfg.CaptureLimit {
		return h, redacted // redacted is already sorted
	}
	slices.Sort(names)
	kept := make(http.Header, len(h))
	total = 0
	for _, k := range names {
		size := headerSize(k, h[k])
		if total+size > in.cfg.CaptureLimit {
			redacted = append(redacted, k)
			continue
		}
		total += size
		kept[k] = h[k]
	}
	slices.Sort(redacted)
	return kept, redacted
}

// headerSize is the bytes of one header's name and values.
func headerSize(name string, values []string) int {
	n := len(name)
	for _, v := range values {
		n += len(v)
	}
	return n
}

// TruncateQuery returns q cut to the inspector's capture limit, in bytes,
// and whether it was cut.
func (in *Inspector) TruncateQuery(q string) (string, bool) {
	if len(q) > in.cfg.CaptureLimit {
		return q[:in.cfg.CaptureLimit], true
	}
	return q, false
}

// NewBuffer returns a buffer for one body. It keeps bytes only if the hook
// captures bodies and the relay allows it.
func (in *Inspector) NewBuffer(hookCapture bool) *Buffer {
	return newBuffer(in.cfg.CaptureLimit, hookCapture && !in.cfg.NoBodies)
}

// Add records a new call and drops the hook's oldest calls beyond
// CallsPerHook. Refused calls are kept apart, up to refusedPerHook, and
// only ever drop each other. Past TotalBytes, the oldest calls of any hook
// are dropped.
func (in *Inspector) Add(c Call) {
	in.mu.Lock()
	defer in.mu.Unlock()
	p := &c
	ring, limit := in.byHook, in.cfg.CallsPerHook
	if c.Refused {
		ring, limit = in.refused, refusedPerHook
	}
	list := append(ring[c.HookID], p)
	if over := len(list) - limit; over > 0 {
		for _, old := range list[:over] {
			in.forgetLocked(old)
		}
		list = slices.Delete(list, 0, over)
	}
	ring[c.HookID] = list
	in.byID[c.ID] = p
	in.order = append(in.order, p)
	in.resizeLocked(p)
	in.changedLocked(p)
}

// Update changes a recorded call. It does nothing if the call was dropped.
func (in *Inspector) Update(id string, f func(*Call)) {
	in.mu.Lock()
	defer in.mu.Unlock()
	c, ok := in.byID[id]
	if !ok {
		return
	}
	f(c)
	in.resizeLocked(c)
	in.changedLocked(c)
}

// Bytes is what the kept calls take, as counted against TotalBytes.
func (in *Inspector) Bytes() int64 {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.total
}

// resizeLocked counts c's size again, and evicts the oldest calls while
// all of them take more than TotalBytes.
func (in *Inspector) resizeLocked(c *Call) {
	size := callBytes(c)
	in.total += size - in.sizes[c.ID]
	in.sizes[c.ID] = size
	for in.total > in.cfg.TotalBytes && len(in.order) > 0 {
		old := in.order[0]
		in.order = in.order[1:]
		if in.byID[old.ID] != old {
			continue // dropped already
		}
		ring := in.byHook
		if old.Refused {
			ring = in.refused
		}
		if list := slices.DeleteFunc(ring[old.HookID], func(x *Call) bool { return x == old }); len(list) > 0 {
			ring[old.HookID] = list
		} else {
			delete(ring, old.HookID)
		}
		in.forgetLocked(old)
	}
	// order also holds calls dropped by their hook's limit or Forget;
	// compact it once they are the majority.
	if len(in.order) > 2*len(in.byID)+64 {
		in.order = slices.DeleteFunc(in.order, func(x *Call) bool { return in.byID[x.ID] != x })
	}
}

// forgetLocked stops counting c, which its caller removed from its ring.
func (in *Inspector) forgetLocked(c *Call) {
	delete(in.byID, c.ID)
	in.total -= in.sizes[c.ID]
	delete(in.sizes, c.ID)
}

// callBytes estimates the memory c takes: what it captured, and a fixed
// overhead.
func callBytes(c *Call) int64 {
	n := callOverhead + len(c.Path) + len(c.Query) + headersBytes(c.RequestHeaders) + len(c.RequestBody.Body)
	for _, a := range c.Attempts {
		n += callOverhead + headersBytes(a.ResponseHeaders) + len(a.ResponseBody.Body)
	}
	return int64(n)
}

func headersBytes(h http.Header) int {
	n := 0
	for k, vs := range h {
		n += headerSize(k, vs)
	}
	return n
}

func (in *Inspector) changedLocked(c *Call) {
	if in.OnChange != nil && !c.Refused {
		in.OnChange(c.Owner, c.Channel, c.Summary())
	}
}

// List returns the hook's calls, refused ones included, newest first.
func (in *Inspector) List(hook hooks.ID) []Summary {
	in.mu.Lock()
	defer in.mu.Unlock()
	calls, refused := in.byHook[hook], in.refused[hook]
	out := make([]Summary, 0, len(calls)+len(refused))
	i, j := len(calls)-1, len(refused)-1
	for i >= 0 || j >= 0 {
		if j < 0 || i >= 0 && !refused[j].ReceivedAt.After(calls[i].ReceivedAt) {
			out = append(out, calls[i].Summary())
			i--
		} else {
			out = append(out, refused[j].Summary())
			j--
		}
	}
	return out
}

// Latest returns the hook's newest call that was not refused: refused
// calls are not announced, and must not hide the hook's real traffic.
func (in *Inspector) Latest(hook hooks.ID) (Summary, bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	list := in.byHook[hook]
	if len(list) == 0 {
		return Summary{}, false
	}
	return list[len(list)-1].Summary(), true
}

// Get returns a copy of the call with the given ID.
func (in *Inspector) Get(id string) (Call, bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	c, ok := in.byID[id]
	if !ok {
		return Call{}, false
	}
	return c.clone(), true
}

// Forget drops every call of hook, e.g. because the hook was deleted.
func (in *Inspector) Forget(hook hooks.ID) {
	in.mu.Lock()
	defer in.mu.Unlock()
	for _, c := range slices.Concat(in.byHook[hook], in.refused[hook]) {
		in.forgetLocked(c)
	}
	delete(in.byHook, hook)
	delete(in.refused, hook)
}
