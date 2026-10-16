package main

import (
	"fmt"
	"net/http"

	"github.com/Radiergummi/tsrelay/internal/api"
	"golang.org/x/mod/semver"
)

// version is the relay's version. The release workflow sets it with
// -ldflags "-X main.version=v1.2.3" (see .github/workflows/release.yml
// and the Dockerfile's VERSION build arg). An unmodified build says "dev".
var version = "dev"

// versionCheck passes the relay's version (its api.VersionHeader) to seen
// whenever it differs from this CLI's. A dev build on either side matches
// anything, so it is never passed on.
type versionCheck struct {
	next http.RoundTripper
	seen func(relay string)
}

func (v *versionCheck) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := v.next.RoundTrip(r)
	if err == nil {
		if relay := resp.Header.Get(api.VersionHeader); relay != "" && relay != "dev" && version != "dev" && relay != version {
			v.seen(relay)
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

// warnVersion tells the user on stderr that this CLI and the relay differ,
// and how to install the relay's version.
func (c *cli) warnVersion(relay string) {
	rel := "older than"
	if semver.Compare(version, relay) > 0 {
		rel = "newer than"
	}
	msg := fmt.Sprintf("tsrelay %s is %s the relay (%s); update with: %s", version, rel, relay, c.updateCommand())
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
