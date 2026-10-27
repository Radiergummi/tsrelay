package tunneltest

import (
	"slices"
	"sync"

	"github.com/Radiergummi/tsrelay/internal/tunnel"
)

// Hooks owns a tunnel.Client's hook list the way the CLI does: it drops
// the hooks control messages take away. Use List as the client's List and
// call Follow from its OnControl.
type Hooks struct {
	mu  sync.Mutex
	ids []string
}

func NewHooks(ids ...string) *Hooks { return &Hooks{ids: slices.Clone(ids)} }

func (h *Hooks) List() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.ids)
}

// Follow drops the hooks ctl reports replaced, revoked or deleted.
func (h *Hooks) Follow(ctl tunnel.Control) {
	if !ctl.Type.DropsHooks() {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, info := range ctl.Hooks {
		h.ids = slices.DeleteFunc(h.ids, func(id string) bool { return id == info.ID })
	}
}
