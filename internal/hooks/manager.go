package hooks

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Radiergummi/tsrelay/internal/access"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"tailscale.com/tailcfg"
)

// ErrForbidden means the caller may not own hooks at all.
var ErrForbidden = errors.New("forbidden")

// CreateRequest describes a new hook. Empty Path means three random words;
// empty Channel means authz.DefaultChannel.
type CreateRequest struct {
	Path        string     `json:"path,omitzero"`
	Channel     string     `json:"channel,omitzero"`
	Description string     `json:"description,omitzero"`
	Ephemeral   bool       `json:"ephemeral,omitzero"`
	Public      bool       `json:"public,omitzero"`
	Settings    Settings   `json:"settings,omitzero"`
	ExpiresAt   *time.Time `json:"expiresAt,omitzero"`
}

// Manager holds all hooks in memory, enforces ownership rules, and writes
// changes through to the Store.
type Manager struct {
	store  Store
	slugs  SlugConfig
	policy Policy
	now    func() time.Time

	// Changes take wmu for their whole run, store write included, and mu
	// only to change the fields below. So a slow store write, such as an
	// fsync on a degraded volume, never holds up routing a call. Holding
	// wmu, a change may read hooks and policy without mu: only it writes them.
	wmu   sync.Mutex
	mu    sync.RWMutex
	hooks map[ID]Hook
}

// put records h in memory. The caller holds wmu.
func (m *Manager) put(h Hook) {
	m.mu.Lock()
	m.hooks[h.ID] = h
	m.mu.Unlock()
}

// NewManager loads all hooks from store.
func NewManager(ctx context.Context, store Store, slugs SlugConfig) (*Manager, error) {
	list, err := store.ListHooks(ctx)
	if err != nil {
		return nil, err
	}
	m := &Manager{store: store, slugs: slugs, policy: DefaultPolicy, now: time.Now, hooks: map[ID]Hook{}}
	for _, h := range list {
		m.hooks[h.ID] = h
	}
	return m, nil
}

// Slug returns the URL slug for a login.
func (m *Manager) Slug(login string) (string, error) { return m.slugs.Slug(login) }

// SetPolicy replaces the relay-wide hook setting defaults and maxima.
func (m *Manager) SetPolicy(p Policy) {
	m.wmu.Lock()
	defer m.wmu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policy = p
}

func (m *Manager) Policy() Policy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.policy
}

// Effective resolves a hook's settings against the current relay defaults.
func (m *Manager) Effective(h Hook) Effective { return m.Policy().Resolve(h.Settings) }

// validateHookPath checks path, and that it neither equals nor nests inside the
// path of one of owned other than the hook except (empty for a new hook).
func validateHookPath(path string, owned []Hook, except ID) []problem.FieldError {
	if err := ValidatePath(path); err != nil {
		return []problem.FieldError{{Field: "path", Message: err.Error()}}
	}
	for _, h := range owned {
		if h.ID != except && overlaps(h.Path, path) {
			return []problem.FieldError{{Field: "path", Message: fmt.Sprintf("conflicts with your hook %q; a hook cannot equal or nest inside another", h.Path)}}
		}
	}
	return nil
}

// validateChannel checks that channel is valid and that grants let its
// owner receive on it.
func validateChannel(channel string, grants authz.Effective) []problem.FieldError {
	switch {
	case !authz.ValidChannel(channel):
		return []problem.FieldError{{Field: "channel", Message: fmt.Sprintf("%q is not a valid channel name (dot-separated [a-z0-9-] segments, not ending in .json or .html)", channel)}}
	case !access.MayReceive(grants, channel):
		return []problem.FieldError{{Field: "channel", Message: fmt.Sprintf("channel %q is not matched by your receiver patterns %s",
			channel, strings.Join(grants.Patterns[authz.RoleReceiver], ", "))}}
	}
	return nil
}

func validateDescription(description string) []problem.FieldError {
	if len(description) > maxDescription {
		return []problem.FieldError{{Field: "description", Message: fmt.Sprintf("must be at most %d characters", maxDescription)}}
	}
	if strings.ContainsFunc(description, Unprintable) {
		return []problem.FieldError{{Field: "description", Message: "must be one line without control or bidirectional formatting characters"}}
	}
	return nil
}

// Unprintable reports whether r may change how a terminal shows the text
// around it: a control character (C0, DEL, C1, which start escape
// sequences) or a bidirectional formatting character.
func Unprintable(r rune) bool {
	return unicode.IsControl(r) || r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069' || r == '\u200e' || r == '\u200f' || r == '\u061c'
}

// validateExpiry checks expiresAt against now and the owner's maxHookTtl.
// Ephemeral hooks need no expiry, because they end with their tunnel.
func validateExpiry(expiresAt *time.Time, ephemeral bool, now time.Time, maxTTL time.Duration) []problem.FieldError {
	switch {
	case expiresAt == nil && maxTTL > 0 && !ephemeral:
		return []problem.FieldError{{Field: "expiresAt", Message: fmt.Sprintf("your grants require persistent hooks to expire within %s", maxTTL)}}
	case expiresAt == nil:
		return nil
	case !expiresAt.After(now):
		return []problem.FieldError{{Field: "expiresAt", Message: "must be in the future"}}
	case maxTTL > 0 && expiresAt.Sub(now) > maxTTL:
		return []problem.FieldError{{Field: "expiresAt", Message: fmt.Sprintf("must be within %s from now (your grants' maxHookTtl)", maxTTL)}}
	}
	return nil
}

func (m *Manager) Create(ctx context.Context, owner authz.Identity, req CreateRequest) (Hook, error) {
	return m.create(ctx, owner, req, false)
}

// CheckCreate validates req exactly as Create does, slug binding included,
// but saves nothing. It returns the hook Create would make, without an ID.
func (m *Manager) CheckCreate(ctx context.Context, owner authz.Identity, req CreateRequest) (Hook, error) {
	return m.create(ctx, owner, req, true)
}

func (m *Manager) create(ctx context.Context, owner authz.Identity, req CreateRequest, dry bool) (Hook, error) {
	if !access.MayCreate(owner) {
		return Hook{}, ErrForbidden
	}
	slug, err := m.slugs.Slug(owner.Login)
	if err != nil {
		return Hook{}, problem.FieldErrors{{Field: "owner", Message: err.Error()}}
	}
	if req.Channel == "" {
		req.Channel = authz.DefaultChannel
	}
	if req.ExpiresAt != nil {
		req.ExpiresAt = new(req.ExpiresAt.UTC()) // timestamps are UTC, whatever offset the client sent
	}

	m.wmu.Lock()
	defer m.wmu.Unlock()
	owned := m.ownedLocked(owner.UserID)
	var errs problem.FieldErrors
	if req.Path == "" {
		req.Path = m.freePathLocked(owned)
	} else {
		errs = append(errs, validateHookPath(req.Path, owned, "")...)
	}
	errs = append(errs, validateChannel(req.Channel, owner.Grants)...)
	errs = append(errs, validateDescription(req.Description)...)
	errs = append(errs, m.policy.Validate(req.Settings)...)
	errs = append(errs, validateExpiry(req.ExpiresAt, req.Ephemeral, m.now(), owner.Grants.Limits.MaxHookTTL)...)
	if req.Public {
		errs = append(errs, validatePublic(owner.Grants, req.Channel)...)
	}
	if len(owned) >= owner.Grants.Limits.MaxHooks {
		errs = append(errs, problem.FieldError{Field: "", Message: fmt.Sprintf("you already own %d hooks, the maximum your grants allow", len(owned))})
	}
	if len(errs) > 0 {
		return Hook{}, errs
	}

	bind := m.store.BindSlug
	if dry {
		bind = m.slugFree
	}
	if err := bind(ctx, slug, owner.UserID); err != nil {
		if errors.Is(err, ErrSlugTaken) {
			return Hook{}, problem.FieldErrors{{Field: "owner", Message: fmt.Sprintf(
				"the URL prefix ~%s belongs to another user; ask the relay operator to configure a slug override for %s", slug, owner.Login)}}
		}
		return Hook{}, err
	}
	h := Hook{
		ID: NewID(), Owner: owner.UserID, OwnerLogin: owner.Login, Slug: slug,
		Path: req.Path, Channel: req.Channel, Description: req.Description,
		Ephemeral: req.Ephemeral, Public: req.Public, Settings: req.Settings, ExpiresAt: req.ExpiresAt, CreatedAt: m.now().UTC(),
	}
	if h.Public {
		h.PublicToken = newPublicToken()
	}
	if dry {
		h.ID = ""
		return h, nil
	}
	if err := m.store.PutHook(ctx, h); err != nil {
		return Hook{}, err
	}
	m.put(h)
	return h, nil
}

// slugFree fails with ErrSlugTaken if slug is bound to a user other than
// user. It is BindSlug without the binding, for dry runs.
func (m *Manager) slugFree(ctx context.Context, slug string, user tailcfg.UserID) error {
	owner, ok, err := m.store.SlugOwner(ctx, slug)
	if err != nil {
		return err
	}
	if ok && owner != user {
		return ErrSlugTaken
	}
	return nil
}

// freePathLocked picks a random path that does not overlap the owner's hooks.
func (m *Manager) freePathLocked(owned []Hook) string {
	for {
		p := RandomPath()
		if !slices.ContainsFunc(owned, func(h Hook) bool { return overlaps(h.Path, p) }) {
			return p
		}
	}
}

// ownedLocked needs mu or wmu.
func (m *Manager) ownedLocked(user tailcfg.UserID) []Hook {
	var out []Hook
	for _, h := range m.hooks {
		if h.Owner == user {
			out = append(out, h)
		}
	}
	return out
}

func (m *Manager) Get(id ID) (Hook, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	h, ok := m.hooks[id]
	return h, ok
}

// List returns the hooks owned by user, oldest first.
func (m *Manager) List(user tailcfg.UserID) []Hook {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return sortHooks(m.ownedLocked(user))
}

// ListAll returns every hook, oldest first.
func (m *Manager) ListAll() []Hook {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return sortHooks(slices.Collect(maps.Values(m.hooks)))
}

func sortHooks(hs []Hook) []Hook {
	slices.SortFunc(hs, func(a, b Hook) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), strings.Compare(string(a.ID), string(b.ID)))
	})
	return hs
}

// DeleteIf deletes the hook id if cond holds for it as stored, deciding
// and deleting under one lock so a concurrent change cannot slip between
// the two. cond runs without mu held, so it may take locks that are held
// while calling Get. It returns the stored hook and whether it was deleted.
// Only the lifecycle's Delete and Reap may call it: they keep the tunnels
// serving the hook and the calls waiting for it consistent with the store.
func (m *Manager) DeleteIf(ctx context.Context, id ID, cond func(Hook) bool) (Hook, bool, error) {
	m.wmu.Lock()
	defer m.wmu.Unlock()
	h, ok := m.hooks[id]
	if !ok {
		return Hook{}, false, ErrNotFound
	}
	if !cond(h) {
		return h, false, nil
	}
	if err := m.store.DeleteHook(ctx, id); err != nil {
		return h, false, err
	}
	m.mu.Lock()
	delete(m.hooks, id)
	m.mu.Unlock()
	return h, true, nil
}

// Route finds the hook serving rest, the decoded request path below
// "/~{slug}/". It returns the hook and how many path segments belong to it.
// Hooks never nest, so at most one hook matches. The scan is linear in the
// number of hooks, which is fine at the scale of one relay.
func (m *Manager) Route(slug, rest string) (Hook, int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := m.now()
	for _, h := range m.hooks {
		if h.Slug == slug && (rest == h.Path || strings.HasPrefix(rest, h.Path+"/")) {
			if h.Expired(now) {
				return Hook{}, 0, false
			}
			return h, strings.Count(h.Path, "/") + 1, true
		}
	}
	return Hook{}, 0, false
}
