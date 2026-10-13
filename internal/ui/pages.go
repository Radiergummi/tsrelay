package ui

import (
	"cmp"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"

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
	if last, ok := u.api.Inspect.Latest(h.ID); ok {
		r.Last = &last
	}
	return r
}

// hooksPage lists the caller's hooks, or explains why they have none.
func (u *UI) hooksPage(w http.ResponseWriter, r *http.Request, v viewer) {
	if !v.Receiver() {
		if v.Admin() {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}
		u.noAccess(w, v)
		return
	}
	list := u.api.Hooks.List(v.ID.UserID)
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
	h, ok := u.api.VisibleHook(hooks.ID(r.PathValue("id")), v.ID)
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
	}{v, hook, v.owns(h), u.api.Inspect.List(h.ID), settingRows(hook)})
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
		{"Rate limit", fmt.Sprintf("%s/s, burst %d", strconv.FormatFloat(e.RateLimit.PerSecond, 'f', -1, 64), e.RateLimit.Burst), s.RateLimit == nil},
		{"Capture", capture, s.Capture == nil},
	}
}

// inspectRedirect resolves the inspect link the CLI prints for each call
// (DESIGN.md §15) to the call's page.
func (u *UI) inspectRedirect(w http.ResponseWriter, r *http.Request, v viewer) {
	if c, ok := u.api.Inspect.Get(r.PathValue("callID")); ok {
		if _, visible := u.api.VisibleHook(c.HookID, v.ID); visible {
			http.Redirect(w, r, "/hooks/"+url.PathEscape(string(c.HookID))+"/calls/"+url.PathEscape(c.ID), http.StatusSeeOther)
			return
		}
	}
	u.error(w, v, http.StatusNotFound, noCall)
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

// adminPage lists every hook as metadata, with its owner and tunnel, so
// admins can see what is attached and delete hooks (DESIGN.md §5.2).
func (u *UI) adminPage(w http.ResponseWriter, r *http.Request, v viewer) {
	if !v.Admin() {
		u.error(w, v, http.StatusForbidden, "Only relay admins can see this page.")
		return
	}
	all := u.api.Hooks.ListAll()
	slices.SortFunc(all, func(a, b hooks.Hook) int {
		return cmp.Or(cmp.Compare(a.OwnerLogin, b.OwnerLogin), cmp.Compare(a.Path, b.Path))
	})
	rows := make([]hookRow, len(all))
	tunnels := map[string]bool{}
	for i, h := range all {
		rows[i] = u.row(h)
		if t := rows[i].Tunnel; t != nil {
			tunnels[t.ID] = true
		}
	}
	u.render(w, http.StatusOK, "admin", struct {
		V       viewer
		Hooks   []hookRow
		Tunnels int
	}{v, rows, len(tunnels)})
}
