package hooks

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Radiergummi/tsrelay/internal/authz"
)

// Opt is a JSON field of a partial update: absent leaves the value alone,
// null resets it, and a value replaces it.
type Opt[T any] struct {
	Set   bool
	Value *T
}

func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

func (o Opt[T]) MarshalJSON() ([]byte, error) { return json.Marshal(o.Value) }

// IsZero makes `omitzero` leave unset fields out of marshalled requests.
func (o Opt[T]) IsZero() bool { return !o.Set }

type SettingsPatch struct {
	AttachPolicy    Opt[AttachPolicy] `json:"attachPolicy,omitzero"`
	HostHeader      Opt[HostHeader]   `json:"hostHeader,omitzero"`
	HoldTimeout     Opt[Duration]     `json:"holdTimeout,omitzero"`
	ResponseTimeout Opt[Duration]     `json:"responseTimeout,omitzero"`
	IdleTimeout     Opt[Duration]     `json:"idleTimeout,omitzero"`
	MaxBodyBytes    Opt[int64]        `json:"maxBodyBytes,omitzero"`
	RateLimit       Opt[RateLimit]    `json:"rateLimit,omitzero"`
	Capture         Opt[bool]         `json:"capture,omitzero"`
}

// UpdateRequest is a partial update of a hook (PATCH semantics).
type UpdateRequest struct {
	Path        Opt[string]    `json:"path,omitzero"`
	Channel     Opt[string]    `json:"channel,omitzero"`
	Description Opt[string]    `json:"description,omitzero"`
	Ephemeral   Opt[bool]      `json:"ephemeral,omitzero"`
	Public      Opt[bool]      `json:"public,omitzero"`
	ExpiresAt   Opt[time.Time] `json:"expiresAt,omitzero"`
	Settings    SettingsPatch  `json:"settings"`
}

// apply sets dst from o if o was given; it reports whether it did.
func apply[T any](dst **T, o Opt[T]) bool {
	if o.Set {
		*dst = o.Value
	}
	return o.Set
}

// apply sets the fields of s that p gives and returns their names.
func (p SettingsPatch) apply(s *Settings) []string {
	var changed []string
	touch := func(name string, set bool) {
		if set {
			changed = append(changed, "settings."+name)
		}
	}
	touch("attachPolicy", apply(&s.AttachPolicy, p.AttachPolicy))
	touch("hostHeader", apply(&s.HostHeader, p.HostHeader))
	touch("holdTimeout", apply(&s.HoldTimeout, p.HoldTimeout))
	touch("responseTimeout", apply(&s.ResponseTimeout, p.ResponseTimeout))
	touch("idleTimeout", apply(&s.IdleTimeout, p.IdleTimeout))
	touch("maxBodyBytes", apply(&s.MaxBodyBytes, p.MaxBodyBytes))
	touch("rateLimit", apply(&s.RateLimit, p.RateLimit))
	touch("capture", apply(&s.Capture, p.Capture))
	return changed
}

// Update applies req to the hook id owned by owner. It returns the updated
// hook and the names of the fields the request touched. Hooks of other
// users are reported as ErrNotFound, so their existence is not revealed.
func (m *Manager) Update(ctx context.Context, owner authz.Identity, id ID, req UpdateRequest) (Hook, []string, error) {
	return m.update(ctx, owner, id, req, false)
}

// CheckUpdate validates req exactly as Update does but saves nothing. It
// returns the hook as Update would leave it.
func (m *Manager) CheckUpdate(ctx context.Context, owner authz.Identity, id ID, req UpdateRequest) (Hook, []string, error) {
	return m.update(ctx, owner, id, req, true)
}

func (m *Manager) update(ctx context.Context, owner authz.Identity, id ID, req UpdateRequest, dry bool) (Hook, []string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.hooks[id]
	if !ok || !owner.IsUser() || cur.Owner != owner.UserID {
		return Hook{}, nil, ErrNotFound
	}
	h := cur
	var errs ValidationError
	var changed []string
	touch := func(name string, set bool) {
		if set {
			changed = append(changed, name)
		}
	}

	if req.Path.Set {
		touch("path", true)
		if req.Path.Value == nil {
			errs = append(errs, FieldError{"path", "cannot be null"})
		} else {
			h.Path = *req.Path.Value
			errs = append(errs, validateHookPath(h.Path, m.ownedLocked(owner.UserID), id)...)
		}
	}
	if req.Channel.Set {
		touch("channel", true)
		h.Channel = authz.DefaultChannel
		if req.Channel.Value != nil {
			h.Channel = *req.Channel.Value
		}
		errs = append(errs, validateChannel(h.Channel, owner.Grants)...)
	}
	if req.Description.Set {
		touch("description", true)
		h.Description = ""
		if req.Description.Value != nil {
			h.Description = *req.Description.Value
		}
		errs = append(errs, validateDescription(h.Description)...)
	}
	if req.Ephemeral.Set {
		touch("ephemeral", true)
		if req.Ephemeral.Value == nil {
			errs = append(errs, FieldError{"ephemeral", "cannot be null"})
		} else {
			h.Ephemeral = *req.Ephemeral.Value
		}
	}
	if req.Public.Set {
		touch("public", true)
		switch {
		case req.Public.Value == nil:
			errs = append(errs, FieldError{"public", "cannot be null"})
		case *req.Public.Value && !cur.Public:
			errs = append(errs, validatePublic(owner.Grants)...)
			h.Public, h.PublicToken = true, newPublicToken()
		case !*req.Public.Value:
			h.Public, h.PublicToken = false, ""
		}
	}
	if touch("expiresAt", apply(&h.ExpiresAt, req.ExpiresAt)); req.ExpiresAt.Set || req.Ephemeral.Set {
		errs = append(errs, validateExpiry(h.ExpiresAt, h.Ephemeral, m.now(), owner.Grants.Limits.MaxHookTTL)...)
	}

	changed = append(changed, req.Settings.apply(&h.Settings)...)
	// Only the settings this request sets are validated: a stored value over
	// a since-lowered maximum must not block unrelated edits (Resolve clamps it).
	var touched Settings
	req.Settings.apply(&touched)
	errs = append(errs, m.policy.Validate(touched)...)

	if len(errs) > 0 {
		return Hook{}, nil, errs
	}
	if dry {
		return h, changed, nil
	}
	if err := m.store.PutHook(ctx, h); err != nil {
		return Hook{}, nil, err
	}
	m.hooks[id] = h
	return h, changed, nil
}

// expiredLocked reports whether h has passed its expiry.
func (m *Manager) expiredLocked(h Hook, now time.Time) bool {
	return h.Expired(now)
}
