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
	return c.stdoutStream(), c.stderrStream()
}

// stderrStream returns stderr alone, for reporting an error.
func (c *cli) stderrStream() stream { return newStream(c.stderr, c.noColor, c.noLinks) }

// stdoutStream returns stdout alone, for commands that write nothing else.
func (c *cli) stdoutStream() stream { return newStream(c.stdout, c.noColor, c.noLinks) }

func hookKind(h api.Hook) string {
	kind := "persistent"
	if h.Ephemeral {
		kind = "ephemeral"
	}
	if h.Public {
		kind += ", public"
	}
	return kind
}

func hookExpiry(h api.Hook) string {
	if h.ExpiresAt == nil {
		return "-"
	}
	return h.ExpiresAt.Local().Format(time.DateTime)
}

// writeHookList prints hooks with writeTable.
func writeHookList(out, errStream stream, hs []api.Hook) {
	header := []string{"ID", "PATH", "CHANNEL", "KIND", "TUNNEL", "EXPIRES", "URL"}
	rows := make([][]string, len(hs))
	for i, h := range hs {
		tun := "-"
		if h.Tunnel != nil {
			tun = h.Tunnel.Node
		}
		rows[i] = []string{h.ID, h.Path, h.Channel, hookKind(h), tun, hookExpiry(h), out.link(h.URL, h.URL)}
	}
	writeTable(out, errStream, header, rows, "no hooks yet; tsrelay http <port> creates one")
}

// writeTable prints rows as a table on out when out is fancy, else as
// tab-separated lines with a header, for cut and awk. Without rows, a
// fancy out gets no table: the hint empty goes to errStream instead, since
// stdout carries only the data.
func writeTable(out, errStream stream, header []string, rows [][]string, empty string) {
	if !out.fancy {
		fmt.Fprintln(out.w, strings.Join(header, "\t"))
		for _, r := range rows {
			fmt.Fprintln(out.w, strings.Join(r, "\t"))
		}
		return
	}
	if len(rows) == 0 {
		fmt.Fprintln(errStream.w, errStream.style(styleDim, empty))
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
	fmt.Fprintln(out.w, t.String())
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
		{"public url", orDash(h.PublicURL)},
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
