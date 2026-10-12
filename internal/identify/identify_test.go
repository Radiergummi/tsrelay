package identify

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
	"tailscale.com/tailcfg/peercap"
)

const testCap peercap.Cap = "mazetti.me/cap/relay"

func whois(tags []string, caps ...string) *apitype.WhoIsResponse {
	vals := make([]tailcfg.RawMessage, len(caps))
	for i, c := range caps {
		vals[i] = tailcfg.RawMessage(c)
	}
	return &apitype.WhoIsResponse{
		Node:        &tailcfg.Node{StableID: "n1", Name: "laptop.tail-scale.ts.net.", Tags: tags},
		UserProfile: &tailcfg.UserProfile{ID: 42, LoginName: "moritz@example.com"},
		CapMap:      tailcfg.PeerCapMap{testCap: vals},
	}
}

func TestFromWhoIsUser(t *testing.T) {
	id, errs := FromWhoIs(whois(nil, `{"roles":["receiver"]}`), testCap, authz.DefaultLimits)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if id.UserID != 42 || id.Login != "moritz@example.com" || id.NodeName != "laptop" || id.NodeID != "n1" {
		t.Fatalf("identity = %+v", id)
	}
	if !id.IsUser() || !id.Grants.Has(authz.RoleReceiver) {
		t.Fatalf("want receiving user, got %+v", id)
	}
}

func TestFromWhoIsTaggedHasNoUser(t *testing.T) {
	id, _ := FromWhoIs(whois([]string{"tag:prod-worker"}, `{"roles":["sender"]}`), testCap, authz.DefaultLimits)
	if id.IsUser() || id.Login != "" || !id.UserID.IsZero() {
		t.Fatalf("tagged node must not carry a user: %+v", id)
	}
	if !id.Grants.Has(authz.RoleSender) {
		t.Fatal("tagged sender lost its role")
	}
}

func TestFromWhoIsOtherCapIgnored(t *testing.T) {
	res := whois(nil)
	res.CapMap = tailcfg.PeerCapMap{"example.com/cap/other": {`{"roles":["admin"]}`}}
	id, _ := FromWhoIs(res, testCap, authz.DefaultLimits)
	if id.Grants.Has(authz.RoleAdmin) {
		t.Fatal("roles from another capability must not apply")
	}
}

type staticWhoIs struct{ res *apitype.WhoIsResponse }

func (s staticWhoIs) WhoIs(context.Context, string) (*apitype.WhoIsResponse, error) {
	return s.res, nil
}

func TestTailscaleLogsParseErrorsOnce(t *testing.T) {
	var buf bytes.Buffer
	ts := &Tailscale{
		Client:   staticWhoIs{whois(nil, `garbage`, `{"roles":["receiver"]}`)},
		Cap:      testCap,
		Defaults: authz.DefaultLimits,
		Logger:   slog.New(slog.NewJSONHandler(&buf, nil)),
	}
	for range 3 {
		id, err := ts.Identify(context.Background(), "100.64.0.1:1234")
		if err != nil || !id.Grants.Has(authz.RoleReceiver) {
			t.Fatalf("Identify = %+v, %v", id, err)
		}
	}
	if n := strings.Count(buf.String(), "cap.parse_error"); n != 1 {
		t.Fatalf("logged %d parse errors, want 1:\n%s", n, buf.String())
	}
}

func TestFakeIdentifiesDialedConnections(t *testing.T) {
	f := &Fake{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := f.Identify(r.Context(), r.RemoteAddr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		io.WriteString(w, id.NodeName)
	}))
	defer srv.Close()

	for _, node := range []string{"alice-laptop", "worker"} {
		resp, err := f.Client(Tagged(node, []string{"tag:x"})).Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != node {
			t.Fatalf("server identified %q, want %q", body, node)
		}
	}
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unregistered connection: status %d, want 403", resp.StatusCode)
	}
}

func TestBuildersParseCaps(t *testing.T) {
	u := User("laptop", "a@example.com", 7, `{"roles":["receiver"],"channels":["prod"]}`)
	if !u.IsUser() || !u.Grants.Allows(authz.RoleReceiver, "prod") || u.Grants.Allows(authz.RoleReceiver, "dev") {
		t.Fatalf("User() = %+v", u)
	}
	tg := Tagged("worker", []string{"tag:prod-worker"}, `{"roles":["sender"]}`)
	if tg.IsUser() || !tg.Grants.Has(authz.RoleSender) {
		t.Fatalf("Tagged() = %+v", tg)
	}
}
