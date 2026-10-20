// Command licensetexts harvests the license and notice texts of every
// component in the SBOM into internal/sbom/generated/licensetexts.json, and
// renders them after tsrelay's NOTICE into
// internal/sbom/generated/notices.txt. Run by scripts/build-sbom.sh from the
// repository root.
package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Radiergummi/tsrelay/internal/sbom"
)

const (
	sbomPath    = "internal/sbom/generated/sbom.cdx.json"
	textsPath   = "internal/sbom/generated/licensetexts.json"
	noticesPath = "internal/sbom/generated/notices.txt"
	preamble    = "NOTICE"
	nodeModules = "web/node_modules/.pnpm"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "licensetexts:", err)
		os.Exit(1)
	}
}

func run() error {
	raw, err := os.ReadFile(sbomPath)
	if err != nil {
		return err
	}
	doc, err := sbom.Project(raw)
	if err != nil {
		return fmt.Errorf("project %s: %w", sbomPath, err)
	}
	out, err := exec.Command("go", "env", "GOMODCACHE", "GOROOT").Output()
	if err != nil {
		return fmt.Errorf("go env: %w", err)
	}
	modCache, goRoot, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	// Homebrew moves the toolchain's LICENSE from GOROOT to its parent.
	if _, err := os.Stat(filepath.Join(goRoot, "LICENSE")); err != nil {
		goRoot = filepath.Dir(goRoot)
	}
	artifact, err := Harvest(doc, Roots{GoModCache: modCache, GoRoot: goRoot, NodeModules: nodeModules})
	if err != nil {
		return err
	}
	// Map keys are sorted, so the file is the same on every run.
	encoded, err := json.MarshalIndent(artifact, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(textsPath, append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	notice, err := os.ReadFile(preamble)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d components, %d distinct texts)\n", textsPath, len(artifact.Components), len(artifact.Texts))
	return os.WriteFile(noticesPath, []byte(renderNotices(doc, artifact, strings.TrimRight(string(notice), "\n"))), 0o644)
}

// renderNotices is the notices document: tsrelay's NOTICE, then each
// component with its licenses, license text and any NOTICE it ships.
func renderNotices(doc sbom.Document, artifact sbom.Artifact, preamble string) string {
	var out strings.Builder
	out.WriteString(preamble + "\n\n\nBundled dependencies\n====================\n\n")
	out.WriteString("Generated from the software bill of materials by `make sbom`. Do not edit by hand.\n")
	for _, c := range doc.Components {
		entry, ok := artifact.Components[sbom.ComponentKey(c)]
		if !ok {
			continue
		}
		out.WriteString("\n" + strings.Repeat("-", 78) + "\n\n")
		out.WriteString(strings.TrimSpace(c.Name+" "+c.Version) + "\n")
		if link := cmp.Or(c.Homepage, c.Repository); link != "" {
			out.WriteString(link + "\n")
		}
		var ids []string
		for _, l := range c.Licenses {
			if id := cmp.Or(l.ID, l.Name); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			out.WriteString("License: " + strings.Join(ids, ", ") + "\n")
		}
		out.WriteString("\n" + artifact.Texts[entry.License] + "\n")
		if entry.Notice != "" {
			out.WriteString("\nNOTICE:\n\n" + artifact.Texts[entry.Notice] + "\n")
		}
	}
	return out.String()
}
