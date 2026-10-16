package sbom

import (
	_ "embed"
	"encoding/json"
)

// The artifacts `make sbom` writes; see scripts/build-sbom.sh. Tests check
// they parse and agree, so decoding them at startup cannot fail.
var (
	//go:embed sbom.cdx.json
	raw []byte
	//go:embed licensetexts.json
	rawTexts []byte
	//go:embed notices.txt
	notices []byte
)

var (
	texts         = mustDecode[Artifact](rawTexts)
	projectedJSON = mustProject(raw)
)

func mustDecode[T any](b []byte) T {
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		panic("sbom: " + err.Error())
	}
	return v
}

func mustProject(raw []byte) []byte {
	doc, err := Project(raw)
	if err != nil {
		panic("sbom: " + err.Error())
	}
	// Only the served document carries text IDs: the generator that writes
	// the pool reads Project's output and must not see the previous IDs.
	attachTexts(doc.Components)
	b, _ := json.Marshal(doc) // strings and slices only; cannot fail
	return b
}

// Raw is the CycloneDX SBOM, GET /sbom.cdx.json.
func Raw() []byte { return raw }

// ProjectedJSON is the licenses document as JSON, GET /licenses.
func ProjectedJSON() []byte { return projectedJSON }

// Notices is every bundled dependency's license and notice text after
// tsrelay's own NOTICE, GET /notices.txt and `tsrelay licenses`.
func Notices() []byte { return notices }

// Text returns the license or notice text with the given ID.
func Text(id string) (string, bool) {
	text, ok := texts.Texts[id]
	return text, ok
}

// attachTexts sets each component's license and notice text IDs. A
// component of an ecosystem without harvested texts keeps empty IDs.
func attachTexts(components []Component) {
	for i, c := range components {
		if entry, ok := texts.Components[ComponentKey(c)]; ok {
			components[i].TextID, components[i].NoticeID = entry.License, entry.Notice
		}
	}
}
