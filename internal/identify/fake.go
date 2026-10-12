package identify

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"tailscale.com/tailcfg"
)

// Fake is an Identifier for tests. A connection is identified by the local
// address of the dialer that opened it, so tests can give each simulated
// node its own identity on a single loopback address.
type Fake struct {
	mu     sync.Mutex
	byAddr map[string]authz.Identity
	err    error
}

func (f *Fake) Identify(_ context.Context, remoteAddr string) (authz.Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return authz.Identity{}, f.err
	}
	id, ok := f.byAddr[remoteAddr]
	if !ok {
		return authz.Identity{}, fmt.Errorf("fake: unknown peer %s", remoteAddr)
	}
	return id, nil
}

// Fail makes every Identify return err until Fail(nil), as if WhoIs failed.
func (f *Fake) Fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// Update replaces the identity of every connection from id's node, as if
// the node's grants had changed in the tailnet policy file.
func (f *Fake) Update(id authz.Identity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for addr, cur := range f.byAddr {
		if cur.NodeID == id.NodeID {
			f.byAddr[addr] = id
		}
	}
}

// Dial returns a DialContext function whose connections are identified as id.
func (f *Fake) Dial(id authz.Identity) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		c, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		f.mu.Lock()
		if f.byAddr == nil {
			f.byAddr = map[string]authz.Identity{}
		}
		f.byAddr[c.LocalAddr().String()] = id
		f.mu.Unlock()
		return c, nil
	}
}

// Client returns an HTTP/1.1 client whose requests are identified as id.
// Compression is disabled so tests see bytes exactly as sent.
func (f *Fake) Client(id authz.Identity) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: f.Dial(id), DisableCompression: true}}
}

// User builds a user identity whose grants are parsed from capability JSON
// values, as WhoIs would deliver them. It panics on invalid values.
func User(node, login string, userID int64, caps ...string) authz.Identity {
	id := build(node, caps)
	id.UserID = tailcfg.UserID(userID)
	id.Login = login
	return id
}

// Tagged builds a tagged-node identity; see User.
func Tagged(node string, tags []string, caps ...string) authz.Identity {
	id := build(node, caps)
	id.Tags = tags
	return id
}

func build(node string, caps []string) authz.Identity {
	vals := make([]tailcfg.RawMessage, len(caps))
	for i, c := range caps {
		vals[i] = tailcfg.RawMessage(c)
	}
	grants, errs := authz.Parse(vals, authz.DefaultLimits)
	if len(errs) > 0 {
		panic(fmt.Sprintf("identify: invalid test capability: %v", errs))
	}
	return authz.Identity{NodeID: tailcfg.StableNodeID("n-" + node), NodeName: node, Grants: grants}
}
