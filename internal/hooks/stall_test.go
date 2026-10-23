package hooks

import (
	"context"
	"testing"
	"time"
)

// stallingStore blocks every write until release is closed.
type stallingStore struct {
	Store
	writing chan struct{}
	release chan struct{}
}

func (s stallingStore) PutHook(c context.Context, h Hook) error {
	s.writing <- struct{}{}
	<-s.release
	return s.Store.PutHook(c, h)
}

func (s stallingStore) DeleteHook(c context.Context, id ID) error {
	s.writing <- struct{}{}
	<-s.release
	return s.Store.DeleteHook(c, id)
}

// A slow store write, such as an fsync on a degraded volume, must not hold
// up routing the calls of other hooks.
func TestStoreWritesDoNotBlockReads(t *testing.T) {
	m := newManager(t, t.TempDir())
	served, err := m.Create(ctx, receiver, CreateRequest{Path: "served"})
	if err != nil {
		t.Fatal(err)
	}
	st := stallingStore{Store: m.store, writing: make(chan struct{}), release: make(chan struct{})}
	m.store = st
	defer close(st.release)
	for name, write := range map[string]func(){
		"create": func() { m.Create(ctx, receiver, CreateRequest{Path: "new"}) },
		"update": func() {
			m.Update(ctx, receiver, served.ID, UpdateRequest{Description: Opt[string]{Set: true, Value: new("x")}})
		},
		"delete": func() { m.DeleteIf(ctx, served.ID, func(Hook) bool { return true }) },
	} {
		go write()
		<-st.writing // the write is now stalled in the store
		done := make(chan struct{})
		go func() {
			m.Route("moritz", "served/x")
			m.Get(served.ID)
			m.RoutePublic("t")
			m.Policy()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s: reads wait for the stalled store write", name)
		}
		st.release <- struct{}{} // let this one write finish
	}
}
