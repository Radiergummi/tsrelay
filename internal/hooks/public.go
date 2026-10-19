package hooks

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"

	"github.com/Radiergummi/tsrelay/internal/authz"
)

// ErrNotPublic means a public-only operation was asked of a private hook.
var ErrNotPublic = errors.New("the hook is not public")

// newPublicToken returns 128 random bits as 22 base64url characters. At
// 128 bits, two hooks never draw the same token.
func newPublicToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	return base64.RawURLEncoding.EncodeToString(b)
}

// validatePublic checks that grants let the owner make hooks public.
func validatePublic(grants authz.Effective) []FieldError {
	if grants.Public {
		return nil
	}
	return []FieldError{{"public", `your grants do not allow public hooks; a receiver grant needs "public": true`}}
}

// RoutePublic finds the public hook with token. Like Route, it scans every
// hook, which is fine at the scale of one relay.
func (m *Manager) RoutePublic(token string) (Hook, bool) {
	if token == "" {
		return Hook{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, h := range m.hooks {
		// The token works like a password: compare it in constant time.
		if h.Public && subtle.ConstantTimeCompare([]byte(h.PublicToken), []byte(token)) == 1 {
			if h.Expired(m.now()) {
				return Hook{}, false
			}
			return h, true
		}
	}
	return Hook{}, false
}

// RotatePublicToken gives the public hook id owned by owner a new token, so
// its old public URL stops working.
func (m *Manager) RotatePublicToken(ctx context.Context, owner authz.Identity, id ID) (Hook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.hooks[id]
	if !ok || !owner.IsUser() || h.Owner != owner.UserID {
		return Hook{}, ErrNotFound
	}
	if !h.Public {
		return Hook{}, ErrNotPublic
	}
	h.PublicToken = newPublicToken()
	if err := m.store.PutHook(ctx, h); err != nil {
		return Hook{}, err
	}
	m.hooks[id] = h
	return h, nil
}
