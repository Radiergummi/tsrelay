// Package client is the developer-machine side of tsrelay: it talks to the
// relay API and forwards tunnelled calls to a local target.
package client

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ParseTarget turns "8080", ":8080", "localhost:8080" or an http(s) URL into
// the URL calls are forwarded to.
func ParseTarget(s string) (*url.URL, error) {
	if n, err := strconv.Atoi(strings.TrimPrefix(s, ":")); err == nil {
		if n < 1 || n > 65535 {
			return nil, fmt.Errorf("invalid port %d", n)
		}
		return &url.URL{Scheme: "http", Host: fmt.Sprintf("localhost:%d", n)}, nil
	}
	raw := s
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("invalid target %q: want a port or an http(s) URL", s)
	}
	return u, nil
}
