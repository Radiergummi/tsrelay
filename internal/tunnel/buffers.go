package tunnel

import "sync"

// Buffers is an httputil.BufferPool for the reverse proxies on both ends
// of a tunnel. Without one, every response copy allocates a fresh 32 KiB
// buffer.
var Buffers = &bufferPool{}

type bufferPool struct{ p sync.Pool }

func (b *bufferPool) Get() []byte {
	if buf, ok := b.p.Get().(*[]byte); ok {
		return *buf
	}
	return make([]byte, 32<<10) // the size ReverseProxy uses on its own
}

func (b *bufferPool) Put(buf []byte) { b.p.Put(&buf) }
