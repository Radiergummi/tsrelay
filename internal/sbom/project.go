// Package sbom embeds the CycloneDX software bill of materials that `make
// sbom` generates (scripts/build-sbom.sh), the license and notice texts of
// its components, and the notices document, and projects the SBOM into the
// licenses document the web UI's licenses page reads (DESIGN.md §10.1).
package sbom

import (
	"bytes"
	"cmp"
	"slices"
	"strings"

	cyclonedx "github.com/CycloneDX/cyclonedx-go"
)

// Document is the licenses document, GET /licenses.
type Document struct {
	Components []Component `json:"components"`
}

// Component is one open-source dependency built into tsrelay.
type Component struct {
	Name        string    `json:"name"`
	Version     string    `json:"version,omitempty"`
	Description string    `json:"description,omitempty"`
	Ecosystem   string    `json:"ecosystem"` // "go", "npm" or "other"
	Licenses    []License `json:"licenses"`
	Homepage    string    `json:"homepage,omitempty"`
	Repository  string    `json:"repository,omitempty"`
	TextID      string    `json:"textId,omitempty"`
	NoticeID    string    `json:"noticeId,omitempty"`
}

// License is one of a component's licenses.
type License struct {
	ID   string `json:"id,omitempty"` // an SPDX identifier or expression, when known
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Project flattens a CycloneDX JSON document into a Document, sorted by
// ecosystem, then name, then version. Components without a package URL
// are containers: skipped, but walked. It leaves the text IDs empty;
// attachTexts adds them for the served document.
func Project(raw []byte) (Document, error) {
	var bom cyclonedx.BOM
	if err := cyclonedx.NewBOMDecoder(bytes.NewReader(raw), cyclonedx.BOMFileFormatJSON).Decode(&bom); err != nil {
		return Document{}, err
	}
	seen := make(map[string]bool)
	components := []Component{} // [], not null, when empty
	var walk func(in *[]cyclonedx.Component)
	walk = func(in *[]cyclonedx.Component) {
		if in == nil {
			return
		}
		for _, c := range *in {
			if ecosystem := ecosystemOf(c.PackageURL); ecosystem != "" {
				projected := Component{
					Name:        qualifiedName(c),
					Version:     c.Version,
					Description: c.Description,
					Ecosystem:   ecosystem,
					Licenses:    projectLicenses(componentLicenses(c)),
					Homepage:    externalReference(c.ExternalReferences, cyclonedx.ERTypeWebsite),
					Repository:  externalReference(c.ExternalReferences, cyclonedx.ERTypeVCS),
				}
				// The key includes the ecosystem: a Go module and an npm
				// package can share a name and version.
				if key := ComponentKey(projected); !seen[key] {
					seen[key] = true
					components = append(components, projected)
				}
			}
			walk(c.Components)
		}
	}
	walk(bom.Components)
	slices.SortFunc(components, func(a, b Component) int {
		return cmp.Or(
			cmp.Compare(a.Ecosystem, b.Ecosystem),
			cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			cmp.Compare(a.Version, b.Version),
		)
	})
	return Document{Components: components}, nil
}

// qualifiedName rejoins the group CycloneDX splits off a scoped npm
// package's name ("@base-ui" and "react"); alone, the name is ambiguous.
func qualifiedName(c cyclonedx.Component) string {
	if c.Group == "" {
		return c.Name
	}
	return c.Group + "/" + c.Name
}

// ecosystemOf names a package URL's ecosystem, and "" when there is none.
func ecosystemOf(purl string) string {
	switch {
	case purl == "":
		return ""
	case strings.HasPrefix(purl, "pkg:golang/"):
		return "go"
	case strings.HasPrefix(purl, "pkg:npm/"):
		return "npm"
	}
	return "other"
}

// componentLicenses returns the declared licenses, or the detected ones:
// cyclonedx-gomod records a Go module's under evidence.licenses.
func componentLicenses(c cyclonedx.Component) *cyclonedx.Licenses {
	if c.Licenses != nil && len(*c.Licenses) > 0 {
		return c.Licenses
	}
	if c.Evidence != nil {
		return c.Evidence.Licenses
	}
	return nil
}

func projectLicenses(in *cyclonedx.Licenses) []License {
	out := []License{} // [], not null, when empty
	if in == nil {
		return out
	}
	for _, choice := range *in {
		switch {
		case choice.License != nil:
			out = append(out, License{ID: choice.License.ID, Name: choice.License.Name, URL: choice.License.URL})
		case choice.Expression != "":
			out = append(out, License{ID: choice.Expression})
		}
	}
	return out
}

func externalReference(refs *[]cyclonedx.ExternalReference, kind cyclonedx.ExternalReferenceType) string {
	if refs == nil {
		return ""
	}
	for _, ref := range *refs {
		if ref.Type == kind {
			return ref.URL
		}
	}
	return ""
}
