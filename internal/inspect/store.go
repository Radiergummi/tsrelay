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
)

// Config configures the inspector (DESIGN.md §9.2, §12).
type Config struct {
	CallsPerHook int      // calls kept per hook; 0 means DefaultCallsPerHook
	CaptureLimit int      // bytes kept per body; 0 means DefaultCaptureLimit
	NoBodies     bool     // turns body capture off relay-wide (--capture-bodies=false)
	Redact       []string // headers never captured; nil means DefaultRedact
}

// Inspector keeps the most recent calls of each hook in memory. Calls are
// lost on restart (DESIGN.md §11).
type Inspector struct {
	cfg    Config
	redact Redactor
	// OnChange, if set, is called after a call was added or changed. It
	// runs with the store's lock held, so changes are reported in order; it
	// must not block or call the Inspector. Set it before first use.
	OnChange func(owner tailcfg.UserID, s Summary)

	mu     sync.Mutex
	byHook map[hooks.ID][]*Call // oldest first
	byID   map[string]*Call
}

func New(cfg Config) *Inspector {
	cfg.CallsPerHook = cmp.Or(cfg.CallsPerHook, DefaultCallsPerHook)
	cfg.CaptureLimit = cmp.Or(cfg.CaptureLimit, DefaultCaptureLimit)
	if cfg.Redact == nil {
		cfg.Redact = DefaultRedact
	}
	return &Inspector{cfg: cfg, redact: NewRedactor(cfg.Redact), byHook: map[hooks.ID][]*Call{}, byID: map[string]*Call{}}
}

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
// CallsPerHook.
func (in *Inspector) Add(c Call) {
	in.mu.Lock()
	defer in.mu.Unlock()
	p := &c
	list := append(in.byHook[c.HookID], p)
	if over := len(list) - in.cfg.CallsPerHook; over > 0 {
		for _, old := range list[:over] {
			delete(in.byID, old.ID)
		}
		list = slices.Delete(list, 0, over)
	}
	in.byHook[c.HookID] = list
	in.byID[c.ID] = p
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
	in.changedLocked(c)
}

func (in *Inspector) changedLocked(c *Call) {
	if in.OnChange != nil {
		in.OnChange(c.Owner, c.Summary())
	}
}

// List returns the hook's calls, newest first.
func (in *Inspector) List(hook hooks.ID) []Summary {
	in.mu.Lock()
	defer in.mu.Unlock()
	list := in.byHook[hook]
	out := make([]Summary, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		out = append(out, list[i].Summary())
	}
	return out
}

// Latest returns the hook's newest call.
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
	for _, c := range in.byHook[hook] {
		delete(in.byID, c.ID)
	}
	delete(in.byHook, hook)
}
