package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/Radiergummi/tsrelay/internal/access"
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
	// A request refuses fields it doesn't know, also inside a value.
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var v T
	if err := dec.Decode(&v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

func (o Opt[T]) MarshalJSON() ([]byte, error) { return json.Marshal(o.Value) }

// ValueType is the type a value of the field decodes into, so a request
// decoder can look inside it for fields it doesn't know.
func (Opt[T]) ValueType() reflect.Type { return reflect.TypeFor[T]() }

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
	Settings    SettingsPatch  `json:"settings,omitzero"`
}

// apply sets dst from o if o was given.
func apply[T any](dst **T, o Opt[T]) {
	if o.Set {
		*dst = o.Value
	}
}

// apply sets the fields of s that p gives.
func (p SettingsPatch) apply(s *Settings) {
	apply(&s.AttachPolicy, p.AttachPolicy)
	apply(&s.HostHeader, p.HostHeader)
	apply(&s.HoldTimeout, p.HoldTimeout)
	apply(&s.ResponseTimeout, p.ResponseTimeout)
	apply(&s.IdleTimeout, p.IdleTimeout)
	apply(&s.MaxBodyBytes, p.MaxBodyBytes)
	apply(&s.RateLimit, p.RateLimit)
	apply(&s.Capture, p.Capture)
}

// samePtr reports whether a and b are both nil or point to equal values.
func samePtr[T comparable](a, b *T) bool {
	return a == b || a != nil && b != nil && *a == *b
}

// changedFields names the fields an update changed from cur to h.
func changedFields(cur, h Hook) []string {
	var changed []string
	differ := func(name string, differs bool) {
		if differs {
			changed = append(changed, name)
		}
	}
	differ("path", h.Path != cur.Path)
	differ("channel", h.Channel != cur.Channel)
	differ("description", h.Description != cur.Description)
	differ("ephemeral", h.Ephemeral != cur.Ephemeral)
	differ("public", h.Public != cur.Public)
	differ("expiresAt", !(h.ExpiresAt == cur.ExpiresAt || h.ExpiresAt != nil && cur.ExpiresAt != nil && h.ExpiresAt.Equal(*cur.ExpiresAt)))
	s, c := h.Settings, cur.Settings
	differ("settings.attachPolicy", !samePtr(s.AttachPolicy, c.AttachPolicy))
	differ("settings.hostHeader", !samePtr(s.HostHeader, c.HostHeader))
	differ("settings.holdTimeout", !samePtr(s.HoldTimeout, c.HoldTimeout))
	differ("settings.responseTimeout", !samePtr(s.ResponseTimeout, c.ResponseTimeout))
	differ("settings.idleTimeout", !samePtr(s.IdleTimeout, c.IdleTimeout))
	differ("settings.maxBodyBytes", !samePtr(s.MaxBodyBytes, c.MaxBodyBytes))
	differ("settings.rateLimit", !samePtr(s.RateLimit, c.RateLimit))
	differ("settings.capture", !samePtr(s.Capture, c.Capture))
	return changed
}

// Update applies req to the hook id owned by owner. It returns the updated
// hook and the names of the fields whose value changed. Hooks of other
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
	m.wmu.Lock()
	defer m.wmu.Unlock()
	cur, ok := m.hooks[id]
	if !ok || access.View(owner, cur.Access()) != access.Owner {
		return Hook{}, nil, ErrNotFound
	}
	h := cur
	var errs ValidationError

	if req.Path.Set {
		if req.Path.Value == nil {
			errs = append(errs, FieldError{"path", "cannot be null"})
		} else {
			h.Path = *req.Path.Value
			errs = append(errs, validateHookPath(h.Path, m.ownedLocked(owner.UserID), id)...)
		}
	}
	if req.Channel.Set {
		h.Channel = authz.DefaultChannel
		if req.Channel.Value != nil {
			h.Channel = *req.Channel.Value
		}
	}
	if req.Description.Set {
		h.Description = ""
		if req.Description.Value != nil {
			h.Description = *req.Description.Value
		}
		errs = append(errs, validateDescription(h.Description)...)
	}
	if req.Ephemeral.Set {
		if req.Ephemeral.Value == nil {
			errs = append(errs, FieldError{"ephemeral", "cannot be null"})
		} else {
			h.Ephemeral = *req.Ephemeral.Value
		}
	}
	if req.Public.Set {
		switch {
		case req.Public.Value == nil:
			errs = append(errs, FieldError{"public", "cannot be null"})
		case *req.Public.Value && !cur.Public:
			h.Public, h.PublicToken = true, newPublicToken()
		case !*req.Public.Value:
			h.Public, h.PublicToken = false, ""
		}
	}
	// The resulting hook is validated, not just the request: a hook left in a
	// channel the owner's grants no longer cover must be moved, not edited in place.
	errs = append(errs, validateChannel(h.Channel, owner.Grants)...)
	if h.Public {
		errs = append(errs, validatePublic(owner.Grants, h.Channel)...)
	}
	if apply(&h.ExpiresAt, req.ExpiresAt); req.ExpiresAt.Set || req.Ephemeral.Set {
		errs = append(errs, validateExpiry(h.ExpiresAt, h.Ephemeral, m.now(), owner.Grants.Limits.MaxHookTTL)...)
	}

	req.Settings.apply(&h.Settings)
	// Only the settings this request sets are validated: a stored value over
	// a since-lowered maximum must not block unrelated edits (Resolve clamps it).
	var touched Settings
	req.Settings.apply(&touched)
	errs = append(errs, m.policy.Validate(touched)...)

	if len(errs) > 0 {
		return Hook{}, nil, errs
	}
	changed := changedFields(cur, h)
	if dry {
		return h, changed, nil
	}
	if err := m.store.PutHook(ctx, h); err != nil {
		return Hook{}, nil, err
	}
	m.put(h)
	return h, changed, nil
}
