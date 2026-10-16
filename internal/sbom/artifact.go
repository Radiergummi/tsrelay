package sbom

// ComponentTexts holds the IDs of one component's license and notice texts.
type ComponentTexts struct {
	License string `json:"license"`
	Notice  string `json:"notice,omitempty"`
}

// Artifact is licensetexts.json: a pool of license and notice texts keyed
// by a hash of their contents, and which component has which. Written by
// scripts/licensetexts and read here, so the shape is declared once.
type Artifact struct {
	Texts      map[string]string         `json:"texts"`
	Components map[string]ComponentTexts `json:"components"`
}

// ComponentKey is a component's key in the artifact: ecosystem, name and
// version, the same identity Project deduplicates on.
func ComponentKey(c Component) string {
	return c.Ecosystem + ":" + c.Name + "@" + c.Version
}
