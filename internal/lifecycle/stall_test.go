package lifecycle

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Radiergummi/tsrelay/internal/audit"
	"github.com/Radiergummi/tsrelay/internal/hooks"
)

// stallingStore blocks every delete until release is closed.
type stallingStore struct {
	hooks.Store
	stall    bool
	deleting chan struct{}
	release  chan struct{}
}

func (s *stallingStore) DeleteHook(ctx context.Context, id hooks.ID) error {
	if s.stall {
		s.deleting <- struct{}{}
		<-s.release
	}
	return s.Store.DeleteHook(ctx, id)
}

// A slow store delete, such as an fsync on a degraded volume, must not hold
// up finding the tunnel of every other call.
func TestStoreDeletesDoNotBlockLookups(t *testing.T) {
	store, err := hooks.OpenJSON(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	st := &stallingStore{Store: store, deleting: make(chan struct{}), release: make(chan struct{})}
	mgr, err := hooks.NewManager(context.Background(), st, hooks.SlugConfig{})
	if err != nil {
		t.Fatal(err)
	}
	l := New(Config{Hooks: mgr, Audit: audit.New(slog.New(slog.NewJSONHandler(io.Discard, nil))), BaseURL: "https://relay"})
	ids := createHooks(t, mgr, 2, hooks.CreateRequest{Ephemeral: true})
	st.stall = true
	defer close(st.release)

	for name, remove := range map[string]func(){
		"delete": func() { l.Delete(context.Background(), receiver, ids[0]) },
		"reap":   func() { l.Reap(context.Background(), 0) },
	} {
		go remove()
		<-st.deleting // the delete is now stalled in the store
		done := make(chan struct{})
		go func() {
			l.Lookup(ids[1])
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s: Lookup waits for the stalled store delete", name)
		}
		st.release <- struct{}{}
	}
}
