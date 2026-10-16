package api

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// Channels is GET /channels: the caller's channel patterns, and the
// channels their hooks are in. The relay knows only the caller's own
// grants (they arrive per request), so it can't say who else may send on
// a channel.
type Channels struct {
	Receive  []string  `json:"receive"`
	Send     []string  `json:"send"`
	Channels []Channel `json:"channels"`
}

// Channel is one channel in use. Hooks lists the caller's hooks in it, and
// is left out for an admin's ?all=true, which counts every user's hooks.
type Channel struct {
	Name      string   `json:"name"`
	HookCount int      `json:"hookCount"`
	Hooks     []Hook   `json:"hooks,omitempty"`
	Receive   []string `json:"receive"` // the caller's receive patterns that match it
}

// ChannelDetail is GET /channels/{name}: the channel's hooks, and what the
// caller may do on it.
type ChannelDetail struct {
	Name       string `json:"name"`
	CanReceive bool   `json:"canReceive"`
	CanSend    bool   `json:"canSend"`
	Hooks      []Hook `json:"hooks"`
}

// scopedHooks are the hooks GET /hooks lists: the caller's, or every
// user's for an admin asking for ?all=true (which non-admins can't).
func (a *API) scopedHooks(r *http.Request, id authz.Identity) (list []hooks.Hook, all bool) {
	switch {
	case r.URL.Query().Get("all") == "true" && id.Grants.Has(authz.RoleAdmin):
		return a.Hooks.ListAll(), true
	case id.IsUser():
		return a.Hooks.List(id.UserID), false
	}
	return nil, false
}

func (a *API) listChannels(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	list, all := a.scopedHooks(r, id)
	byName := map[string]*Channel{}
	for _, h := range list {
		c, ok := byName[h.Channel]
		if !ok {
			c = &Channel{Name: h.Channel, Receive: matching(id.Grants.Patterns[authz.RoleReceiver], h.Channel)}
			byName[h.Channel] = c
		}
		c.HookCount++
		if !all {
			c.Hooks = append(c.Hooks, a.HookJSON(h, id))
		}
	}
	out := Channels{
		Receive:  matching(id.Grants.Patterns[authz.RoleReceiver], ""),
		Send:     matching(id.Grants.Patterns[authz.RoleSender], ""),
		Channels: make([]Channel, 0, len(byName)),
	}
	for _, c := range byName {
		out.Channels = append(out.Channels, *c)
	}
	slices.SortFunc(out.Channels, func(x, y Channel) int { return cmp.Compare(x.Name, y.Name) })
	writeJSON(w, http.StatusOK, out)
}

// getChannel returns a channel's hooks in the caller's scope. A valid
// channel without hooks is still a channel; only an invalid name is 404.
func (a *API) getChannel(w http.ResponseWriter, r *http.Request, id authz.Identity) {
	name := r.PathValue("name")
	if !authz.ValidChannel(name) {
		problem.New(http.StatusNotFound, problem.NotFound, "no such channel; names are dot-separated segments of a-z, 0-9 and -").Write(w)
		return
	}
	list, _ := a.scopedHooks(r, id)
	out := ChannelDetail{
		Name: name, Hooks: []Hook{},
		CanReceive: id.Grants.Allows(authz.RoleReceiver, name),
		CanSend:    id.Grants.Allows(authz.RoleSender, name),
	}
	for _, h := range list {
		if h.Channel == name {
			out.Hooks = append(out.Hooks, a.HookJSON(h, id))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// matching returns the patterns that match channel, or all of them for "".
func matching(patterns []string, channel string) []string {
	out := []string{}
	for _, p := range patterns {
		if channel == "" || authz.MatchChannel(p, channel) {
			out = append(out, p)
		}
	}
	return out
}
