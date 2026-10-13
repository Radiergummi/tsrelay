package ui

import (
	"fmt"
	"net/http"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/inspect"
)

// hookRow is a hook in a list, with its most recent call.
type hookRow struct {
	api.Hook
	Last *inspect.Summary
}

func (u *UI) row(h hooks.Hook) hookRow {
	r := hookRow{Hook: u.api.HookJSON(h)}
	if calls := u.inspect.List(h.ID); len(calls) > 0 {
		r.Last = &calls[0]
	}
	return r
}

// hooksPage lists the caller's hooks, or explains why they have none.
func (u *UI) hooksPage(w http.ResponseWriter, r *http.Request, v viewer) {
	if !v.Receiver() {
		u.noAccess(w, v)
		return
	}
	list := u.hooks.List(v.ID.UserID)
	rows := make([]hookRow, len(list))
	for i, h := range list {
		rows[i] = u.row(h)
	}
	u.render(w, http.StatusOK, "hooks", struct {
		V     viewer
		Hooks []hookRow
		Max   int
	}{v, rows, v.ID.Grants.Limits.MaxHooks})
}

// hookPage shows one hook: its URL, tunnel, calls and settings. Admins see
// other users' hooks too, as metadata.
func (u *UI) hookPage(w http.ResponseWriter, r *http.Request, v viewer) {
	h, ok := u.visibleHook(r.PathValue("id"), v)
	if !ok {
		u.error(w, v, http.StatusNotFound, "There is no hook with this ID, or it is not yours.")
		return
	}
	hook := u.api.HookJSON(h)
	u.render(w, http.StatusOK, "hook", struct {
		V        viewer
		Hook     api.Hook
		Owner    bool
		Calls    []inspect.Summary
		Settings []settingRow
	}{v, hook, v.owns(h), u.inspect.List(h.ID), settingRows(hook)})
}

// settingRow is one effective setting of a hook.
type settingRow struct {
	Name, Value string
	Default     bool // the hook leaves it at the relay default
}

// settingRows lists a hook's effective settings (DESIGN.md §8).
func settingRows(h api.Hook) []settingRow {
	s, e := h.Settings, h.Effective
	hold := formatDuration(e.HoldTimeout)
	if e.HoldTimeout == 0 {
		hold = "off"
	}
	body := "unlimited"
	if e.MaxBodyBytes > 0 {
		body = formatSize(e.MaxBodyBytes)
	}
	capture := "bodies and metadata"
	if !e.Capture {
		capture = "metadata only"
	}
	return []settingRow{
		{"Attach policy", string(e.AttachPolicy), s.AttachPolicy == nil},
		{"Host header", string(e.HostHeader), s.HostHeader == nil},
		{"Hold timeout", hold, s.HoldTimeout == nil},
		{"Response timeout", formatDuration(e.ResponseTimeout), s.ResponseTimeout == nil},
		{"Idle timeout", formatDuration(e.IdleTimeout), s.IdleTimeout == nil},
		{"Max body size", body, s.MaxBodyBytes == nil},
		{"Rate limit", fmt.Sprintf("%g/s, burst %d", e.RateLimit.PerSecond, e.RateLimit.Burst), s.RateLimit == nil},
		{"Capture", capture, s.Capture == nil},
	}
}

// noAccess tells a caller who cannot receive callbacks what is missing,
// with a grant that would fix it (DESIGN.md §5.2).
func (u *UI) noAccess(w http.ResponseWriter, v viewer) {
	u.render(w, http.StatusForbidden, "noaccess", struct {
		V      viewer
		Tagged bool
		Roles  []string
		Cap    string
	}{v, !v.ID.IsUser(), sortedRoles(v.ID.Grants), u.capName})
}
