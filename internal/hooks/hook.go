// Package hooks holds the hook model, its validation, and its storage.
package hooks

import (
	"time"

	"github.com/Radiergummi/tsrelay/internal/ids"
	"tailscale.com/tailcfg"
)

// ID identifies a hook. It is opaque and never part of the hook's URL.
type ID string

func NewID() ID { return ID(ids.New("h_")) }

// Hook is a callback endpoint owned by a tailnet user.
type Hook struct {
	ID          ID             `json:"id"`
	Owner       tailcfg.UserID `json:"owner"`
	OwnerLogin  string         `json:"ownerLogin"`
	Slug        string         `json:"slug"`
	Path        string         `json:"path"`
	Channel     string         `json:"channel"`
	Description string         `json:"description,omitempty"`
	Ephemeral   bool           `json:"ephemeral"`
	// Public hooks are also served on the internet, through Funnel, at
	// PublicRoute. PublicToken is set exactly when Public is.
	Public      bool       `json:"public,omitempty"`
	PublicToken string     `json:"publicToken,omitempty"`
	Settings    Settings   `json:"settings,omitzero"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// Route is the URL path the hook is served at, e.g. "/~moritz/amber-otter-lamp".
func (h Hook) Route() string { return "/~" + h.Slug + "/" + h.Path }

// PublicRoute is the path the hook is served at on the internet, e.g.
// "/+Xq3…". The token works like a password.
func (h Hook) PublicRoute() string { return "/+" + h.PublicToken }
