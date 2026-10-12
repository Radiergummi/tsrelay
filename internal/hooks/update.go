package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
}

// UpdateRequest is a partial update of a hook (PATCH semantics).
type UpdateRequest struct {
	Path        Opt[string]    `json:"path,omitzero"`
	Channel     Opt[string]    `json:"channel,omitzero"`
	Description Opt[string]    `json:"description,omitzero"`
	Ephemeral   Opt[bool]      `json:"ephemeral,omitzero"`
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

// Update applies req to the hook id owned by owner. It returns the updated
// hook and the names of the fields the request touched. Hooks of other
// users are reported as ErrNotFound, so their existence is not revealed.
func (m *Manager) Update(ctx context.Context, owner authz.Identity, id ID, req UpdateRequest) (Hook, []string, error) {
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
		switch {
		case req.Path.Value == nil:
			errs = append(errs, FieldError{"path", "cannot be null"})
		case ValidatePath(*req.Path.Value) != nil:
			errs = append(errs, FieldError{"path", ValidatePath(*req.Path.Value).Error()})
		default:
			h.Path = *req.Path.Value
			for _, o := range m.ownedLocked(owner.UserID) {
				if o.ID != id && overlaps(o.Path, h.Path) {
					errs = append(errs, FieldError{"path", fmt.Sprintf("conflicts with your hook %q; a hook cannot equal or nest inside another", o.Path)})
					break
				}
			}
		}
	}
	if req.Channel.Set {
		touch("channel", true)
		h.Channel = authz.DefaultChannel
		if req.Channel.Value != nil {
			h.Channel = *req.Channel.Value
		}
		switch {
		case !authz.ValidChannel(h.Channel):
			errs = append(errs, FieldError{"channel", fmt.Sprintf("%q is not a valid channel name (dot-separated [a-z0-9-] segments)", h.Channel)})
		case !owner.Grants.Allows(authz.RoleReceiver, h.Channel):
			errs = append(errs, FieldError{"channel", fmt.Sprintf("channel %q is not matched by your receiver patterns %s",
				h.Channel, strings.Join(owner.Grants.Patterns[authz.RoleReceiver], ", "))})
		}
	}
	if req.Description.Set {
		touch("description", true)
		h.Description = ""
		if req.Description.Value != nil {
			h.Description = *req.Description.Value
		}
		if len(h.Description) > maxDescription {
			errs = append(errs, FieldError{"description", fmt.Sprintf("must be at most %d characters", maxDescription)})
		}
	}
	if req.Ephemeral.Set {
		touch("ephemeral", true)
		if req.Ephemeral.Value == nil {
			errs = append(errs, FieldError{"ephemeral", "cannot be null"})
		} else {
			h.Ephemeral = *req.Ephemeral.Value
		}
	}
	if touch("expiresAt", apply(&h.ExpiresAt, req.ExpiresAt)); req.ExpiresAt.Set || req.Ephemeral.Set {
		errs = append(errs, validateExpiry(h.ExpiresAt, h.Ephemeral, m.now(), owner.Grants.Limits.MaxHookTTL)...)
	}

	s, p := &h.Settings, req.Settings
	touch("settings.attachPolicy", apply(&s.AttachPolicy, p.AttachPolicy))
	touch("settings.hostHeader", apply(&s.HostHeader, p.HostHeader))
	touch("settings.holdTimeout", apply(&s.HoldTimeout, p.HoldTimeout))
	touch("settings.responseTimeout", apply(&s.ResponseTimeout, p.ResponseTimeout))
	touch("settings.idleTimeout", apply(&s.IdleTimeout, p.IdleTimeout))
	touch("settings.maxBodyBytes", apply(&s.MaxBodyBytes, p.MaxBodyBytes))
	touch("settings.rateLimit", apply(&s.RateLimit, p.RateLimit))
	errs = append(errs, m.policy.Validate(h.Settings)...)

	if len(errs) > 0 {
		return Hook{}, nil, errs
	}
	if err := m.store.PutHook(ctx, h); err != nil {
		return Hook{}, nil, err
	}
	m.hooks[id] = h
	return h, changed, nil
}

// expiredLocked reports whether h has passed its expiry.
func (m *Manager) expiredLocked(h Hook, now time.Time) bool {
	return h.ExpiresAt != nil && !h.ExpiresAt.After(now)
}
