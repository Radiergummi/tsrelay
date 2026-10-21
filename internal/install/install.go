// Package install serves what a tailnet user needs to get the CLI: install
// scripts for POSIX shells and PowerShell, and redirects to the release
// binaries. Everything points at the GitHub release of the relay's own
// version, or the latest release on a dev build. The binaries are public,
// so none of this identifies the caller.
package install

import (
	"bytes"
	"embed"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"text/template"

	"github.com/Radiergummi/tsrelay/internal/problem"
)

//go:embed install.sh.tmpl install.ps1.tmpl
var files embed.FS

var scripts = template.Must(template.ParseFS(files, "*.tmpl"))

const github = "https://github.com/Radiergummi/tsrelay/releases"

// targets are the platforms the release workflow builds.
var targets = map[string][]string{
	"linux":   {"amd64", "arm64"},
	"darwin":  {"amd64", "arm64"},
	"windows": {"amd64", "arm64"},
}

// DefaultRelayHost is the relay's default MagicDNS host name, where the CLI
// looks for it unless told otherwise.
const DefaultRelayHost = "tsrelay"

type Handler struct {
	Version  string // the relay's version, or "dev" for a build without one
	BaseURL  string // the relay's URL on the tailnet, no trailing slash
	Releases string // base URL of the release assets; "" means GitHub's
}

// Shell serves install.sh, rendered once: nothing in it changes for the
// relay's lifetime.
func (h *Handler) Shell() http.Handler {
	return h.script("install.sh.tmpl", "text/x-shellscript; charset=utf-8")
}

// PowerShell serves install.ps1, rendered once.
func (h *Handler) PowerShell() http.Handler {
	return h.script("install.ps1.tmpl", "text/plain; charset=utf-8")
}

// Download redirects GET /download/{os}/{arch} to that build of the
// relay's release.
func (h *Handler) Download() http.Handler { return http.HandlerFunc(h.download) }

func (h *Handler) dev() bool { return h.Version == "dev" }

// assetBase is the URL the release assets are below.
func (h *Handler) assetBase() string {
	releases := h.Releases
	if releases == "" {
		releases = github
	}
	if h.dev() {
		return releases + "/latest/download"
	}
	return releases + "/download/" + h.Version
}

// relayHint is the TSRELAY_RELAY the CLI needs to find this relay, or ""
// when it is where the CLI looks by default: https://tsrelay.<tailnet>.
func (h *Handler) relayHint() string {
	u, err := url.Parse(h.BaseURL)
	if err == nil && u.Scheme == "https" {
		if label, _, _ := strings.Cut(u.Hostname(), "."); label == DefaultRelayHost {
			return ""
		}
	}
	return h.BaseURL
}

func (h *Handler) script(name, contentType string) http.HandlerFunc {
	version := h.Version
	if h.dev() {
		version = "latest"
	}
	var buf bytes.Buffer
	if err := scripts.ExecuteTemplate(&buf, name, map[string]string{
		"AssetBase": h.assetBase(),
		"Version":   version,
		"RelayURL":  h.BaseURL,
		"RelayHint": h.relayHint(),
	}); err != nil {
		panic(err) // the templates are embedded, so this is a build defect
	}
	body := buf.Bytes()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(body)
	}
}

func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	goos, arch := r.PathValue("os"), r.PathValue("arch")
	if !slices.Contains(targets[goos], arch) {
		problem.New(http.StatusNotFound, problem.NotFound, "there is no tsrelay build for "+goos+"/"+arch).Write(w)
		return
	}
	name := "tsrelay_" + goos + "_" + arch
	if goos == "windows" {
		name += ".exe"
	}
	http.Redirect(w, r, h.assetBase()+"/"+name, http.StatusFound)
}
