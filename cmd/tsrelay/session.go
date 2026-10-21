package main

import (
	"time"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/client"
)

// sessionEvents renders s's events on out and keeps the background run
// record current; hold is how long the relay holds calls while the tunnel
// is down. onChange, if set, runs for every hook change (http remembers it).
func (c *cli) sessionEvents(s *client.Session, out *output, hold time.Duration, onChange func(api.Hook)) func(client.Event) {
	target := s.Target.String()
	onState := out.onState(target, hold)
	return func(e client.Event) {
		switch e.Kind {
		case client.EventConnected:
			onState(true, nil)
			if c.bg != nil {
				c.bg.ready(c.runRecord(s.Current(), s.Target.Redacted()))
			}
		case client.EventLost:
			onState(false, e.Err)
		case client.EventHookChanged:
			// Recorded before it is printed, so whoever reads the URL finds
			// the record and the remembered path already updated.
			c.bg.hookChanged(e.OldID, e.Hook)
			if onChange != nil {
				onChange(e.Hook)
			}
			out.url(e.Hook, e.Recreated)
		case client.EventHookDropped:
			c.bg.hookDropped(e.Hook.ID)
			out.dropped(e.Hook, e.Reason)
		case client.EventCall:
			out.call(e.Call)
		case client.EventUpstreamError:
			out.upstreamError(e.Err)
		}
	}
}
