// Command sbomnpm renders the npm half of the CycloneDX SBOM from the web
// UI's pnpm lockfile, which is the only place that records the tarball
// hashes. Run by scripts/build-sbom.sh from the repository root.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	cyclonedx "github.com/CycloneDX/cyclonedx-go"
)

// Where pnpm keeps the web UI's lockfile and unpacked packages, relative to
// the repository root, and the importer whose production dependencies ship.
const (
	lockPath  = "web/pnpm-lock.yaml"
	importer  = "."
	storeRoot = "web/node_modules/.pnpm"
	refPrefix = "web" // each component's bom-ref is web|name@version
)

func main() {
	out := flag.String("out", "", "output file (default stdout)")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "sbomnpm:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	lock, err := loadLockfile(lockPath, importer)
	if err != nil {
		return err
	}
	store, err := openStore(storeRoot)
	if err != nil {
		return err
	}
	keys := lock.closure(importer)
	components := make([]cyclonedx.Component, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		name, version := splitKey(key)
		if name == "" || version == "" {
			continue
		}
		// One package resolved against two sets of peers is two snapshot
		// keys but one component: a bom-ref carries no peer suffix.
		id := name + "@" + version
		if seen[id] {
			continue
		}
		seen[id] = true
		// A link: or file: dependency has no registry tarball and no entry here.
		pkg, ok := lock.Packages[id]
		if !ok {
			continue
		}
		// A package built for another platform is in the lockfile but not on
		// disk. Say so rather than drop it quietly.
		rendered, err := component(store, refPrefix, name, version, pkg.Resolution.Integrity)
		if errors.Is(err, errNotInstalled) {
			fmt.Fprintf(os.Stderr, "sbomnpm: not installed here, omitted: %s@%s\n", name, version)
			continue
		}
		if err != nil {
			return err
		}
		components = append(components, rendered)
	}
	slices.SortFunc(components, func(a, b cyclonedx.Component) int { return strings.Compare(a.BOMRef, b.BOMRef) })

	// Only the components are used: build-sbom.sh merges them into an
	// envelope of its own without volatile fields.
	encoded, err := json.MarshalIndent(cyclonedx.BOM{
		BOMFormat:   "CycloneDX",
		SpecVersion: cyclonedx.SpecVersion1_6,
		Version:     1,
		Components:  &components,
	}, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if out == "" {
		_, err = os.Stdout.Write(encoded)
		return err
	}
	return os.WriteFile(out, encoded, 0o644)
}
