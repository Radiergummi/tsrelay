package inspect

import (
	"bytes"
	"sync"
)

// Buffer counts every byte of a body written to it and keeps the first
// limit bytes if keep is set. It is safe for concurrent use: a request body
// is read by the transport's goroutine while the handler waits. A nil
// *Buffer discards everything.
type Buffer struct {
	mu    sync.Mutex
	limit int
	keep  bool
	buf   []byte
	size  int64
}

func newBuffer(limit int, keep bool) *Buffer { return &Buffer{limit: limit, keep: keep} }

func (b *Buffer) Write(p []byte) (int, error) {
	if b == nil {
		return len(p), nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.size += int64(len(p))
	if room := b.limit - len(b.buf); b.keep && room > 0 {
		b.buf = append(b.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// Capture returns what the buffer holds. whole reports whether the entire
// body passed through it; a body cut short counts as truncated.
func (b *Buffer) Capture(whole bool) Capture {
	if b == nil {
		return Capture{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return Capture{
		Body:      bytes.Clone(b.buf),
		Size:      b.size,
		Truncated: !whole || (b.keep && b.size > int64(len(b.buf))),
		Captured:  b.keep,
	}
}
