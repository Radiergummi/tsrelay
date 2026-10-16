package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

// lockfile is one document of pnpm's lockfile. pnpm writes several: the
// first records the package manager pinned by packageManager, the
// project's own graph follows, and both name the importer ".".
type lockfile struct {
	Importers map[string]importerEntry `yaml:"importers"`
	Packages  map[string]lockPackage   `yaml:"packages"`
	Snapshots map[string]lockSnapshot  `yaml:"snapshots"`
}

type importerEntry struct {
	Dependencies map[string]struct {
		Version string `yaml:"version"`
	} `yaml:"dependencies"`
}

type lockPackage struct {
	Resolution struct {
		Integrity string `yaml:"integrity"`
	} `yaml:"resolution"`
}

type lockSnapshot struct {
	Dependencies         map[string]string `yaml:"dependencies"`
	OptionalDependencies map[string]string `yaml:"optionalDependencies"`
}

// loadLockfile returns the document in which importer declares dependencies,
// so the package manager's own document is not mistaken for the project's.
func loadLockfile(path, importer string) (*lockfile, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	for {
		var doc lockfile
		switch err := decoder.Decode(&doc); {
		case errors.Is(err, io.EOF):
			return nil, fmt.Errorf("no document in %s declares dependencies for importer %q", path, importer)
		case err != nil:
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if len(doc.Importers[importer].Dependencies) > 0 {
			return &doc, nil
		}
	}
}

// closure walks the production graph from the importer's direct
// dependencies. Development dependencies are not in the entry read, so
// they never enter; optional ones do, because an installed one ships.
func (l *lockfile) closure(importer string) []string {
	seen := map[string]bool{}
	var queue []string
	for name, dep := range l.Importers[importer].Dependencies {
		queue = append(queue, name+"@"+dep.Version)
	}
	for len(queue) > 0 {
		key := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[key] {
			continue
		}
		seen[key] = true
		snapshot := l.Snapshots[key]
		for _, deps := range []map[string]string{snapshot.Dependencies, snapshot.OptionalDependencies} {
			for name, version := range deps {
				queue = append(queue, name+"@"+version)
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	return keys
}

// splitKey separates a snapshot key into package name and version. Keys
// carry the resolved peers in parentheses and the name may be scoped, so
// the version follows the last @ before that suffix.
func splitKey(key string) (name, version string) {
	if cut := strings.IndexByte(key, '('); cut >= 0 {
		key = key[:cut]
	}
	at := strings.LastIndexByte(key, '@')
	if at <= 0 {
		return key, ""
	}
	return key[:at], key[at+1:]
}
