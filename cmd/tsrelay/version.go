package main

import (
	"fmt"
	"net/http"
	"runtime"

	"github.com/Radiergummi/tsrelay/internal/api"
	"golang.org/x/mod/semver"
)

// version is the relay's version. The release workflow sets it with
// -ldflags "-X main.version=v1.2.3" (see .github/workflows/release.yml
// and the Dockerfile's VERSION build arg). An unmodified build says "dev".
var version = "dev"

// commit is the revision the relay was built from, for builds Go cannot
// stamp it into because they have no .git, like the Dockerfile's (its
// REVISION build arg). Elsewhere it stays empty, and Go's stamp is used.
var commit = ""

// skew is how this CLI's version relates to the relay's.
type skew int

const (
	skewNone     skew = iota // the same version, or one that can't be compared
	skewCLIOlder             // the relay is newer within the CLI's major version
	skewCLINewer             // the CLI is newer within the relay's major version
	skewMajor                // different major versions
)

// versionSkew compares the CLI's version with the relay's. A dev build on
// either side, or a version that isn't semver, matches anything.
func versionSkew(cli, relay string) skew {
	if cli == "dev" || relay == "dev" || !semver.IsValid(cli) || !semver.IsValid(relay) {
		return skewNone
	}
	switch c := semver.Compare(cli, relay); {
	case semver.Major(cli) != semver.Major(relay):
		return skewMajor
	case c < 0:
		return skewCLIOlder
	case c > 0:
		return skewCLINewer
	}
	return skewNone
}

// relayTransport tells the relay which tsrelay is calling (User-Agent) and
// passes the relay's version (its api.VersionHeader) to seen whenever it
// is not the CLI's.
type relayTransport struct {
	next http.RoundTripper
	seen func(relay string, skew skew)
}

func (t *relayTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("User-Agent", fmt.Sprintf("tsrelay/%s (%s/%s)", version, runtime.GOOS, runtime.GOARCH))
	resp, err := t.next.RoundTrip(r)
	if err == nil {
		relay := resp.Header.Get(api.VersionHeader)
		if s := versionSkew(version, relay); s != skewNone {
			t.seen(relay, s)
		}
	}
	return resp, err
}

// updateCommand installs the relay's version of tsrelay (see update.go),
// naming the relay as the command line did.
func (c *cli) updateCommand() string {
	if c.relayFrom == "--relay" {
		return "tsrelay update --relay " + c.relay
	}
	return "tsrelay update"
}

// warnVersion tells the user on stderr that this CLI and the relay are
// different major versions, and how to install the relay's version.
func (c *cli) warnVersion(relay string) {
	msg := fmt.Sprintf("tsrelay %s and the relay (%s) are different major versions; install the relay's version with: %s", version, relay, c.updateCommand())
	s := c.stderrStream()
	if c.json {
		writeJSON(s, struct {
			Warning      string `json:"warning"`
			CLIVersion   string `json:"cliVersion"`
			RelayVersion string `json:"relayVersion"`
		}{msg, version, relay})
		return
	}
	fmt.Fprintln(s.w, s.style(styleWarn, "warning: ")+msg)
}
