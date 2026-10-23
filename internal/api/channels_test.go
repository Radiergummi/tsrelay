package api

import (
	"slices"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/hooks"
	"github.com/Radiergummi/tsrelay/internal/problem"
)

// channelEnv has moritz's hooks in default and prod.eu, and other's in staging.
func channelEnv(t *testing.T) *env {
	e := newEnv(t)
	for _, c := range []struct {
		as            authz.Identity
		path, channel string
	}{{moritz, "a", "default"}, {moritz, "b", "prod.eu"}, {moritz, "c", "prod.eu"}, {other, "x", "staging"}} {
		if resp := e.call(c.as, "POST", "/hooks", hooks.CreateRequest{Path: c.path, Channel: c.channel}, nil); resp.StatusCode != 201 {
			t.Fatalf("create %s = %d", c.path, resp.StatusCode)
		}
	}
	return e
}

func channelNames(cs []Channel) []string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Name
	}
	return names
}

func TestListChannels(t *testing.T) {
	e := channelEnv(t)
	var mine Channels
	e.call(moritz, "GET", "/channels", nil, &mine)
	if !slices.Equal(mine.Receive, []string{"default", "prod.*"}) || len(mine.Send) != 0 ||
		!slices.Equal(channelNames(mine.Channels), []string{"default", "prod.eu"}) {
		t.Fatalf("moritz's channels = %+v", mine)
	}
	prod := mine.Channels[1]
	if prod.HookCount != 2 || len(prod.Hooks) != 2 || prod.Hooks[0].Channel != "prod.eu" || !slices.Equal(prod.Receive, []string{"prod.*"}) {
		t.Errorf("prod.eu = %+v", prod)
	}

	// ?all=true is ignored for non-admins, as on /hooks.
	var theirs Channels
	e.call(other, "GET", "/channels?all=true", nil, &theirs)
	if !slices.Equal(channelNames(theirs.Channels), []string{"staging"}) || !slices.Equal(theirs.Channels[0].Receive, []string{"*"}) {
		t.Errorf("other's channels with ?all=true = %+v", theirs)
	}

	var all Channels
	e.call(admin, "GET", "/channels?all=true", nil, &all)
	if !slices.Equal(channelNames(all.Channels), []string{"default", "prod.eu", "staging"}) {
		t.Fatalf("admin's channels = %+v", all)
	}
	counts := map[string]int{"default": 1, "prod.eu": 2, "staging": 1}
	for _, c := range all.Channels {
		if c.Hooks != nil || c.HookCount != counts[c.Name] || len(c.Receive) != 0 {
			t.Errorf("admin's %s = %+v; want a count, no hooks and no receive patterns", c.Name, c)
		}
	}
	var own Channels
	e.call(admin, "GET", "/channels", nil, &own)
	if len(own.Channels) != 0 {
		t.Errorf("admin without ?all=true = %+v", own)
	}

	var w Channels
	if resp := e.call(worker, "GET", "/channels", nil, &w); resp.StatusCode != 200 ||
		!slices.Equal(w.Send, []string{"*"}) || len(w.Receive) != 0 || len(w.Channels) != 0 {
		t.Errorf("tagged sender's channels = %d %+v", resp.StatusCode, w)
	}
}

func TestGetChannel(t *testing.T) {
	e := channelEnv(t)
	var c ChannelDetail
	e.call(moritz, "GET", "/channels/prod.eu", nil, &c)
	if c.Name != "prod.eu" || len(c.Hooks) != 2 || !c.CanReceive || c.CanSend {
		t.Errorf("prod.eu = %+v", c)
	}

	// Another user's channel has none of moritz's hooks, and isn't his to receive on.
	c = ChannelDetail{}
	e.call(moritz, "GET", "/channels/staging?all=true", nil, &c)
	if c.Name != "staging" || len(c.Hooks) != 0 || c.CanReceive {
		t.Errorf("staging for moritz with ?all=true = %+v", c)
	}
	c = ChannelDetail{}
	e.call(admin, "GET", "/channels/staging?all=true", nil, &c)
	if len(c.Hooks) != 1 || c.Hooks[0].Owner != "other@example.com" || c.CanReceive {
		t.Errorf("staging for admin = %+v", c)
	}

	// A valid name without hooks is a channel; an invalid one isn't.
	c = ChannelDetail{}
	if resp := e.call(moritz, "GET", "/channels/nothing.here", nil, &c); resp.StatusCode != 200 || c.Hooks == nil || len(c.Hooks) != 0 {
		t.Errorf("unused channel = %d %+v", resp.StatusCode, c)
	}
	var p problem.Problem
	if resp := e.call(moritz, "GET", "/channels/Not_A_Channel", nil, &p); resp.StatusCode != 404 || p.Code != problem.NotFound {
		t.Errorf("invalid channel = %d %+v", resp.StatusCode, p)
	}

	c = ChannelDetail{}
	e.call(worker, "GET", "/channels/anything", nil, &c)
	if !c.CanSend || c.CanReceive {
		t.Errorf("tagged sender on anything = %+v", c)
	}
}

func TestListHooksByChannel(t *testing.T) {
	e := channelEnv(t)
	for _, tc := range []struct {
		name string
		as   authz.Identity
		path string
		want int
	}{
		{"own channel", moritz, "/hooks?channel=prod.eu", 2},
		{"no hooks in it", moritz, "/hooks?channel=staging", 0},
		{"admin, every user's", admin, "/hooks?all=true&channel=staging", 1},
		{"admin, all channels", admin, "/hooks?all=true", 4},
		{"non-admin ?all=true", other, "/hooks?all=true&channel=prod.eu", 0},
	} {
		var res HookList
		e.call(tc.as, "GET", tc.path, nil, &res)
		if len(res.Hooks) != tc.want {
			t.Errorf("%s: %s = %d hooks, want %d", tc.name, tc.path, len(res.Hooks), tc.want)
		}
	}
}

// A channel's hooks show their public URL to their owner only, as /hooks does.
func TestChannelHidesOthersPublicURL(t *testing.T) {
	e := newEnv(t)
	e.api.PublicBaseURL = publicBase
	var h Hook
	if resp := e.call(publicMoritz, "POST", "/hooks", map[string]any{"path": "cb", "public": true}, &h); resp.StatusCode != 201 || h.PublicURL == "" {
		t.Fatalf("create: %d %+v", resp.StatusCode, h)
	}
	var mine, theirs ChannelDetail
	e.call(publicMoritz, "GET", "/channels/default", nil, &mine)
	e.call(publicAdmin, "GET", "/channels/default?all=true", nil, &theirs)
	if len(mine.Hooks) != 1 || mine.Hooks[0].PublicURL != h.PublicURL {
		t.Errorf("owner sees %+v", mine.Hooks)
	}
	if len(theirs.Hooks) != 1 || !theirs.Hooks[0].Public || theirs.Hooks[0].PublicURL != "" {
		t.Errorf("admin sees %+v; want public without the URL", theirs.Hooks)
	}
}
