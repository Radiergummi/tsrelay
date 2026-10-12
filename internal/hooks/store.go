package hooks

import (
	"context"
	"errors"

	"tailscale.com/tailcfg"
)

var (
	ErrNotFound  = errors.New("hook not found")
	ErrSlugTaken = errors.New("slug is bound to another user")
)

// Store persists hooks and slug bindings. Implementations must be safe for
// concurrent use. The relay keeps its working set in memory (Manager) and
// uses the store as write-through persistence.
type Store interface {
	ListHooks(ctx context.Context) ([]Hook, error)
	GetHook(ctx context.Context, id ID) (Hook, error)
	PutHook(ctx context.Context, h Hook) error // create or replace
	DeleteHook(ctx context.Context, id ID) error
	// BindSlug records that slug belongs to user. Rebinding to the same user
	// succeeds; binding to a different user fails with ErrSlugTaken.
	BindSlug(ctx context.Context, slug string, user tailcfg.UserID) error
	SlugOwner(ctx context.Context, slug string) (tailcfg.UserID, bool, error)
}
