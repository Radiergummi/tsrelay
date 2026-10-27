package tunnel

import "sync"

// Buffers is an httputil.BufferPool for the reverse proxies on both ends
// of a tunnel. Without one, every response copy allocates a fresh 32 KiB
// buffer.
var Buffers = &bufferPool{}

const bufferSize = 32 << 10 // the size ReverseProxy uses on its own

// bufferPool keeps array pointers, which a slice converts to without
// allocating, unlike the *[]byte a pool of slices would need.
type bufferPool struct{ p sync.Pool }

func (b *bufferPool) Get() []byte {
	if buf, ok := b.p.Get().(*[bufferSize]byte); ok {
		return buf[:]
	}
	return make([]byte, bufferSize)
}

func (b *bufferPool) Put(buf []byte) {
	if len(buf) == bufferSize {
		b.p.Put((*[bufferSize]byte)(buf))
	}
}
