package sbom

import (
	"embed"
	"encoding/json"
)

// The artifacts `make sbom` writes into generated/; see
// scripts/build-sbom.sh. They are built, not committed, so a plain go build
// embeds none of them and Available reports false. Tests check that built
// ones parse and agree, so decoding them at startup cannot fail.
//
//go:embed all:generated
var generated embed.FS

var (
	raw      = read("sbom.cdx.json")
	rawTexts = read("licensetexts.json")
	notices  = read("notices.txt")
)

var (
	texts         Artifact
	projectedJSON []byte
)

// Each is decoded on its own: scripts/licensetexts imports this package
// while only the SBOM has been written.
func init() {
	if rawTexts != nil {
		texts = mustDecode[Artifact](rawTexts)
	}
	if raw != nil {
		projectedJSON = mustProject(raw)
	}
}

func read(name string) []byte {
	b, _ := generated.ReadFile("generated/" + name) // absent without `make sbom`
	return b
}

// Available reports whether this build embeds the SBOM and the texts made
// from it. `make build` and the release builds do.
func Available() bool {
	return raw != nil && rawTexts != nil && notices != nil
}

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
