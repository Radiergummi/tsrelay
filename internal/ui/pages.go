package ui

import (
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
