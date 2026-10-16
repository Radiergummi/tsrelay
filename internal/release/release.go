// Package release finds tsrelay's latest release on GitHub, so the relay
// can tell its admins about a newer one (DESIGN.md §10.1).
package release

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

// LatestURL is GitHub's API for tsrelay's latest release.
const LatestURL = "https://api.github.com/repos/Radiergummi/tsrelay/releases/latest"

// Interval is how often Run checks again.
const Interval = 6 * time.Hour

// Release is a published tsrelay version and its release notes.
type Release struct {
	Version string // e.g. "v0.4.0"
	URL     string // the release's page on GitHub
}

// Checker asks GitHub for the latest release and remembers the answer. A
// failed check keeps the last answer.
type Checker struct {
	URL    string       // "" means LatestURL
	HTTP   *http.Client // nil means a client with a 30 s timeout
	Logger *slog.Logger // nil means slog.Default()

	mu     sync.Mutex
	latest Release
}

// Latest is the release the last successful check found; zero before one.
func (c *Checker) Latest() Release {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest
}

// Run checks now and then every Interval until ctx is done.
func (c *Checker) Run(ctx context.Context) {
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		c.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Check asks GitHub once. A failure is logged once and never returned:
// nothing waits for the answer.
func (c *Checker) Check(ctx context.Context) {
	r, err := c.fetch(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.logger().Warn("checking for a newer tsrelay release failed", "error", err)
		}
		return
	}
	c.mu.Lock()
	c.latest = r
	c.mu.Unlock()
}

func (c *Checker) fetch(ctx context.Context) (Release, error) {
	url := c.URL
	if url == "" {
		url = LatestURL
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := hc.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("%s answered %s", url, resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("reading %s: %w", url, err)
	}
	if !semver.IsValid(body.TagName) {
		return Release{}, fmt.Errorf("%s named %q, which is not a version", url, body.TagName)
	}
	return Release{Version: body.TagName, URL: body.HTMLURL}, nil
}

func (c *Checker) logger() *slog.Logger {
	if c.Logger == nil {
		return slog.Default()
	}
	return c.Logger
}
