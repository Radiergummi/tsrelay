package hooks

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"tailscale.com/tailcfg"
)

// ErrForbidden means the caller may not own hooks at all.
var ErrForbidden = errors.New("forbidden")

// FieldError describes why one field of a request was rejected. An empty
// Field means the request as a whole.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError is returned when a request is well-formed but not acceptable.
type ValidationError []FieldError

func (e ValidationError) Error() string {
	msgs := make([]string, len(e))
	for i, fe := range e {
		msgs[i] = fe.Message
		if fe.Field != "" {
			msgs[i] = fe.Field + ": " + fe.Message
		}
	}
	return strings.Join(msgs, "; ")
}

// CreateRequest describes a new hook. Empty Path means three random words;
// empty Channel means authz.DefaultChannel.
type CreateRequest struct {
	Path        string     `json:"path"`
	Channel     string     `json:"channel"`
	Description string     `json:"description"`
	Ephemeral   bool       `json:"ephemeral"`
	Public      bool       `json:"public"`
	Settings    Settings   `json:"settings"`
	ExpiresAt   *time.Time `json:"expiresAt"`
}

// Manager holds all hooks in memory, enforces ownership rules, and writes
// changes through to the Store.
type Manager struct {
	store  Store
	slugs  SlugConfig
	policy Policy
	now    func() time.Time

	mu    sync.RWMutex
	hooks map[ID]Hook
	// detached records when each hook last had no tunnel. Only ephemeral
	// hooks are reaped, but all hooks are tracked to keep the logic simple.
	detached map[ID]time.Time
}

// NewManager loads all hooks from store. Every loaded hook counts as having
// just lost its tunnel, so ephemeral hooks get a full grace period after a
// relay restart.
func NewManager(ctx context.Context, store Store, slugs SlugConfig) (*Manager, error) {
	list, err := store.ListHooks(ctx)
	if err != nil {
		return nil, err
	}
	m := &Manager{store: store, slugs: slugs, policy: DefaultPolicy, now: time.Now, hooks: map[ID]Hook{}, detached: map[ID]time.Time{}}
	start := m.now()
	for _, h := range list {
		m.hooks[h.ID] = h
		m.detached[h.ID] = start
	}
	return m, nil
}

// Slug returns the URL slug for a login.
func (m *Manager) Slug(login string) (string, error) { return m.slugs.Slug(login) }

// SetPolicy replaces the relay-wide hook setting defaults and maxima.
func (m *Manager) SetPolicy(p Policy) {
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
func validateHookPath(path string, owned []Hook, except ID) []FieldError {
	if err := ValidatePath(path); err != nil {
		return []FieldError{{"path", err.Error()}}
	}
	for _, h := range owned {
		if h.ID != except && overlaps(h.Path, path) {
			return []FieldError{{"path", fmt.Sprintf("conflicts with your hook %q; a hook cannot equal or nest inside another", h.Path)}}
		}
	}
	return nil
}

// validateChannel checks that channel is valid and that grants let its
// owner receive on it.
func validateChannel(channel string, grants authz.Effective) []FieldError {
	switch {
	case !authz.ValidChannel(channel):
		return []FieldError{{"channel", fmt.Sprintf("%q is not a valid channel name (dot-separated [a-z0-9-] segments)", channel)}}
	case !grants.Allows(authz.RoleReceiver, channel):
		return []FieldError{{"channel", fmt.Sprintf("channel %q is not matched by your receiver patterns %s",
			channel, strings.Join(grants.Patterns[authz.RoleReceiver], ", "))}}
	}
	return nil
}

func validateDescription(description string) []FieldError {
	if len(description) > maxDescription {
		return []FieldError{{"description", fmt.Sprintf("must be at most %d characters", maxDescription)}}
	}
	return nil
}

// validateExpiry checks expiresAt against now and the owner's maxHookTTL.
// Ephemeral hooks need no expiry, because they end with their tunnel.
func validateExpiry(expiresAt *time.Time, ephemeral bool, now time.Time, maxTTL time.Duration) []FieldError {
	switch {
	case expiresAt == nil && maxTTL > 0 && !ephemeral:
		return []FieldError{{"expiresAt", fmt.Sprintf("your grants require persistent hooks to expire within %s", maxTTL)}}
	case expiresAt == nil:
		return nil
	case !expiresAt.After(now):
		return []FieldError{{"expiresAt", "must be in the future"}}
	case maxTTL > 0 && expiresAt.Sub(now) > maxTTL:
		return []FieldError{{"expiresAt", fmt.Sprintf("must be within %s from now (your grants' maxHookTTL)", maxTTL)}}
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
	if !owner.IsUser() || !owner.Grants.Has(authz.RoleReceiver) {
		return Hook{}, ErrForbidden
	}
	slug, err := m.slugs.Slug(owner.Login)
	if err != nil {
		return Hook{}, ValidationError{{Field: "owner", Message: err.Error()}}
	}
	if req.Channel == "" {
		req.Channel = authz.DefaultChannel
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	owned := m.ownedLocked(owner.UserID)
	var errs ValidationError
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
		errs = append(errs, validatePublic(owner.Grants)...)
	}
	if len(owned) >= owner.Grants.Limits.MaxHooks {
		errs = append(errs, FieldError{"", fmt.Sprintf("you already own %d hooks, the maximum your grants allow", len(owned))})
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
			return Hook{}, ValidationError{{Field: "owner", Message: fmt.Sprintf(
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
	m.hooks[h.ID] = h
	m.detached[h.ID] = m.now()
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
	out := make([]Hook, 0, len(m.hooks))
	for _, h := range m.hooks {
		out = append(out, h)
	}
	return sortHooks(out)
}

func sortHooks(hs []Hook) []Hook {
	slices.SortFunc(hs, func(a, b Hook) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), strings.Compare(string(a.ID), string(b.ID)))
	})
	return hs
}

func (m *Manager) Delete(ctx context.Context, id ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.hooks[id]; !ok {
		return ErrNotFound
	}
	if err := m.store.DeleteHook(ctx, id); err != nil {
		return err
	}
	delete(m.hooks, id)
	delete(m.detached, id)
	return nil
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
			if m.expiredLocked(h, now) {
				return Hook{}, 0, false
			}
			return h, strings.Count(h.Path, "/") + 1, true
		}
	}
	return Hook{}, 0, false
}

// SetAttached records whether a tunnel currently serves the hook.
func (m *Manager) SetAttached(id ID, attached bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.hooks[id]; !ok {
		return
	}
	if attached {
		delete(m.detached, id)
	} else {
		m.detached[id] = m.now()
	}
}

// Reap deletes ephemeral hooks that have had no tunnel for at least grace,
// and every hook whose expiry has passed, and returns them. served reports
// whether a tunnel currently serves a hook;
// it is the source of truth, because a detach can record "detached" after a
// newer tunnel has already attached. Served hooks get a fresh grace period.
func (m *Manager) Reap(ctx context.Context, grace time.Duration, served func(ID) bool) ([]Hook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	var reaped []Hook
	for id, since := range m.detached {
		h := m.hooks[id]
		if !h.Ephemeral || now.Sub(since) < grace {
			continue
		}
		if served(id) {
			m.detached[id] = now
			continue
		}
		if err := m.store.DeleteHook(ctx, id); err != nil {
			return reaped, err
		}
		delete(m.hooks, id)
		delete(m.detached, id)
		reaped = append(reaped, h)
	}
	for id, h := range m.hooks {
		if !m.expiredLocked(h, now) {
			continue
		}
		if err := m.store.DeleteHook(ctx, id); err != nil {
			return reaped, err
		}
		delete(m.hooks, id)
		delete(m.detached, id)
		reaped = append(reaped, h)
	}
	return reaped, nil
}
