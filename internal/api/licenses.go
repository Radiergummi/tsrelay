package api

import (
	"net/http"

	"github.com/Radiergummi/tsrelay/internal/authz"
	"github.com/Radiergummi/tsrelay/internal/problem"
	"github.com/Radiergummi/tsrelay/internal/sbom"
)

// The open-source attribution (package sbom): anyone the relay can identify
// may read it, whatever their roles.

// noSBOM answers for a build without `make sbom`, which has no attribution
// to serve, and reports whether it did.
func noSBOM(w http.ResponseWriter) bool {
	if sbom.Available() {
		return false
	}
	problem.New(http.StatusNotFound, problem.NotFound, "this build of tsrelay has no SBOM; build it with make build").Write(w)
	return true
}

func (a *API) licenses(w http.ResponseWriter, r *http.Request, _ authz.Identity) {
	if noSBOM(w) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(sbom.ProjectedJSON())
}

// licenseText serves one license or notice text. Its ID is a hash of its
// contents, so the response never changes.
func (a *API) licenseText(w http.ResponseWriter, r *http.Request, _ authz.Identity) {
	text, ok := sbom.Text(r.PathValue("textId"))
	if !ok {
		problem.New(http.StatusNotFound, problem.NotFound, "no license text with that ID").Write(w)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write([]byte(text))
}

func (a *API) notices(w http.ResponseWriter, r *http.Request, _ authz.Identity) {
	if noSBOM(w) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(sbom.Notices())
}

func (a *API) sbomDocument(w http.ResponseWriter, r *http.Request, _ authz.Identity) {
	if noSBOM(w) {
		return
	}
	w.Header().Set("Content-Type", "application/vnd.cyclonedx+json; version=1.6")
	w.Write(sbom.Raw())
}
