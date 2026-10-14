// Package web holds the relay's web UI (DESIGN.md §10.2). The app's source
// lives in this directory; `make ui` builds it into dist/, which is
// embedded here. Without a build, dist/ holds only .gitkeep and the relay
// serves a placeholder page instead.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist is the built UI, rooted at dist/.
var Dist = sub(dist, "dist")

func sub(f fs.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err) // only for an invalid path, which "dist" is not
	}
	return s
}
