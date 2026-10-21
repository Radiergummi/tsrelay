package api

import (
	_ "embed"
	"encoding/json"
	"net/http"

	"go.yaml.in/yaml/v3"
)

// openAPIYAML is the API's description (DESIGN.md §10.1), written by hand.
// TestOpenAPIMatchesRoutes keeps it in step with the router.
//
//go:embed openapi.yaml
var openAPIYAML []byte

// openAPIJSON is the same document as JSON, converted once at startup.
var openAPIJSON = mustJSON(openAPIYAML)

func mustJSON(doc []byte) []byte {
	var v any
	if err := yaml.Unmarshal(doc, &v); err != nil {
		panic("openapi.yaml: " + err.Error())
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic("openapi.yaml: " + err.Error())
	}
	return b
}

func serveOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(openAPIJSON)
}

func serveOpenAPIYAML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	w.Write(openAPIYAML)
}
