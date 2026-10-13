package ui

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
)

// formView is the create and edit hook page.
type formView struct {
	V        viewer
	Hook     *api.Hook         // the hook being edited; nil when creating
	Values   map[string]string // each field's initial value, by field name
	Channels []string          // channels the receiver patterns allow as they are
	Patterns string            // the receiver patterns, for the hint
	Policy   hooks.Policy      // relay defaults and maxima, for the hints
	Me       api.Me            // the caller's slug, and when creating their hook count and limits
}

func (u *UI) newHookPage(w http.ResponseWriter, r *http.Request, v viewer) {
	if !v.Receiver() {
		u.noAccess(w, v)
		return
	}
	u.render(w, http.StatusOK, "form", u.formView(v, nil))
}

// editHookPage is for the hook's owner only: admins cannot change other
// users' hooks (DESIGN.md §5.2).
func (u *UI) editHookPage(w http.ResponseWriter, r *http.Request, v viewer) {
	h, ok := u.api.Hooks.Get(hooks.ID(r.PathValue("id")))
	if !ok || !v.owns(h) {
		u.error(w, v, http.StatusNotFound, "There is no hook with this ID, or it is not yours.")
		return
	}
	hook := u.api.HookJSON(h)
	u.render(w, http.StatusOK, "form", u.formView(v, &hook))
}

func (u *UI) formView(v viewer, h *api.Hook) formView {
	patterns := v.ID.Grants.Patterns[authz.RoleReceiver]
	f := formView{V: v, Hook: h, Values: formValues(h), Channels: channelChoices(patterns),
		Patterns: strings.Join(patterns, ", "), Policy: u.api.Hooks.Policy()}
	if h == nil {
		f.Me = u.api.MeOf(v.ID)
	} else {
		f.Me.Slug, _ = u.api.Hooks.Slug(v.ID.Login)
	}
	return f
}

// channelChoices are the channels a receiver may use as they are: "default"
// if a pattern matches it, and every pattern without a wildcard.
func channelChoices(patterns []string) []string {
	var out []string
	if slices.ContainsFunc(patterns, func(p string) bool { return authz.MatchChannel(p, authz.DefaultChannel) }) {
		out = append(out, authz.DefaultChannel)
	}
	for _, p := range patterns {
		if !strings.Contains(p, "*") && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// formValues are the fields' initial values, in the formats the script
// sends: the hook's own values when editing, and empty when creating.
// Settings left at the relay default stay empty, so saving keeps them there.
func formValues(h *api.Hook) map[string]string {
	v := map[string]string{"ephemeral": "false"}
	if h == nil {
		return v
	}
	v["path"], v["channel"], v["description"] = h.Path, h.Channel, h.Description
	v["ephemeral"] = strconv.FormatBool(h.Ephemeral)
	if h.ExpiresAt != nil {
		v["expiresAt"] = h.ExpiresAt.UTC().Format(time.RFC3339)
	}
	s := h.Settings
	if s.AttachPolicy != nil {
		v["settings.attachPolicy"] = string(*s.AttachPolicy)
	}
	if s.HostHeader != nil {
		v["settings.hostHeader"] = string(*s.HostHeader)
	}
	for name, d := range map[string]*hooks.Duration{
		"settings.holdTimeout": s.HoldTimeout, "settings.responseTimeout": s.ResponseTimeout, "settings.idleTimeout": s.IdleTimeout,
	} {
		if d != nil {
			v[name] = d.Std().String()
		}
	}
	if s.MaxBodyBytes != nil {
		v["settings.maxBodyBytes"] = strconv.FormatInt(*s.MaxBodyBytes, 10)
	}
	if s.RateLimit != nil {
		v["settings.rateLimit"] = strconv.FormatFloat(s.RateLimit.PerSecond, 'f', -1, 64) + "/" + strconv.Itoa(s.RateLimit.Burst)
	}
	if s.Capture != nil {
		v["settings.capture"] = strconv.FormatBool(*s.Capture)
	}
	return v
}
