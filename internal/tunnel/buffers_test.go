package tunnel

import "testing"

// Taking a buffer from the pool and returning it allocates nothing. (With
// -race, the pool drops a quarter of what it is given, but AllocsPerRun
// reports the whole number of allocations per run, still 0.)
func TestBuffersDoNotAllocate(t *testing.T) {
	Buffers.Put(Buffers.Get())
	if n := testing.AllocsPerRun(100, func() { Buffers.Put(Buffers.Get()) }); n != 0 {
		t.Fatalf("Get and Put allocate %v times per run, want 0", n)
	}
}
