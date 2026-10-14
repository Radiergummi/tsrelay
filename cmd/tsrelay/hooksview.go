package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"

	"github.com/Radiergummi/tsrelay/internal/api"
)

// streams returns stdout and stderr with this run's styling decisions.
func (c *cli) streams() (out, err stream) {
	return newStream(c.stdout, c.noColor, c.noLinks), newStream(c.stderr, c.noColor, c.noLinks)
}

func hookKind(h api.Hook) string {
	if h.Ephemeral {
		return "ephemeral"
	}
	return "persistent"
}

func hookExpiry(h api.Hook) string {
	if h.ExpiresAt == nil {
		return "-"
	}
	return h.ExpiresAt.Local().Format(time.DateTime)
}

// writeHookList prints hooks as a table when s is fancy, else as
// tab-separated lines with a header, for cut and awk.
func writeHookList(s stream, hs []api.Hook) {
	header := []string{"ID", "PATH", "CHANNEL", "KIND", "TUNNEL", "EXPIRES", "URL"}
	rows := make([][]string, len(hs))
	for i, h := range hs {
		tun := "-"
		if h.Tunnel != nil {
			tun = h.Tunnel.Node
		}
		rows[i] = []string{h.ID, h.Path, h.Channel, hookKind(h), tun, hookExpiry(h), s.link(h.URL, h.URL)}
	}
	if !s.fancy {
		fmt.Fprintln(s.w, strings.Join(header, "\t"))
		for _, r := range rows {
			fmt.Fprintln(s.w, strings.Join(r, "\t"))
		}
		return
	}
	if len(hs) == 0 {
		fmt.Fprintln(s.w, s.style(styleDim, "no hooks yet; tsrelay http <port> creates one"))
		return
	}
	cell := lipgloss.NewStyle().Padding(0, 1)
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(styleDim).
		Headers(header...).
		Rows(rows...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return cell.Bold(true)
			}
			return cell
		})
	fmt.Fprintln(s.w, t.String())
}

// writeHookShow prints one hook and its effective settings as a
// label: value block.
func writeHookShow(s stream, h api.Hook) {
	e := h.Effective
	orDash := func(v string) string {
		if v == "" {
			return "-"
		}
		return v
	}
	tun := "-"
	if h.Tunnel != nil {
		tun = h.Tunnel.Node
		if h.Tunnel.Target != "" {
			tun += " → " + h.Tunnel.Target
		}
	}
	body := "unlimited"
	if e.MaxBodyBytes > 0 {
		body = fmt.Sprint(e.MaxBodyBytes)
	}
	capture := "no"
	if e.Capture {
		capture = "yes"
	}
	fields := [][2]string{
		{"id", h.ID},
		{"url", s.link(h.URL, h.URL)},
		{"path", h.Path},
		{"channel", h.Channel},
		{"kind", hookKind(h)},
		{"description", orDash(h.Description)},
		{"expires", hookExpiry(h)},
		{"tunnel", tun},
		{"hold timeout", e.HoldTimeout.Std().String()},
		{"response timeout", e.ResponseTimeout.Std().String()},
		{"idle timeout", e.IdleTimeout.Std().String()},
		{"max body bytes", body},
		{"rate limit", orDash((&rateLimitFlag{e.RateLimit}).String())},
		{"attach policy", string(e.AttachPolicy)},
		{"host header", string(e.HostHeader)},
		{"capture", capture},
	}
	if !s.fancy {
		for _, f := range fields {
			fmt.Fprintf(s.w, "%s: %s\n", f[0], f[1])
		}
		return
	}
	pad := 0
	for _, f := range fields {
		pad = max(pad, len(f[0]))
	}
	for _, f := range fields {
		fmt.Fprintf(s.w, "  %s  %s\n", s.style(styleDim, fmt.Sprintf("%-*s", pad, f[0])), f[1])
	}
}
