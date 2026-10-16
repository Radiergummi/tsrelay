package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/mod/module"

	"github.com/Radiergummi/tsrelay/internal/sbom"
)

// maxTextBytes caps one license or notice file. Real ones are a few
// kilobytes; anything larger is a misidentified file.
const maxTextBytes = 1 << 20

// licenseStems and noticeStems are the filename prefixes searched for,
// case-insensitively (npm packages often ship "license"), in order: the
// first stem with any match wins.
var (
	licenseStems = []string{"license", "licence", "copying"}
	noticeStems  = []string{"notice"}
)

// rejectedExtensions mark a stem match as source or build metadata rather
// than a text, such as license_test.go beside the real LICENSE.
var rejectedExtensions = map[string]bool{
	"go": true, "js": true, "mjs": true, "cjs": true, "ts": true, "tsx": true,
	"jsx": true, "json": true, "yaml": true, "yml": true, "toml": true,
	"lock": true, "sum": true, "mod": true,
}

func isLicenseCandidate(name string) bool {
	return !rejectedExtensions[strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))]
}

// Roots are the package stores the harvester reads: the Go module cache,
// the directory holding the Go toolchain's LICENSE (the standard library's)
// and pnpm's virtual store. Tests point them at fixtures.
type Roots struct {
	GoModCache  string
	GoRoot      string
	NodeModules string
}

// Harvest resolves every Go and npm component to its source directory and
// pools its license and notice texts, keyed by a hash of their contents.
// A component without a license text fails the harvest.
func Harvest(doc sbom.Document, roots Roots) (sbom.Artifact, error) {
	artifact := sbom.Artifact{Texts: map[string]string{}, Components: map[string]sbom.ComponentTexts{}}
	intern := func(text string) string {
		sum := sha256.Sum256([]byte(text))
		id := hex.EncodeToString(sum[:16])
		artifact.Texts[id] = text
		return id
	}
	for _, c := range doc.Components {
		dir, ok, err := componentDir(c, roots)
		if err != nil {
			return sbom.Artifact{}, err
		}
		if !ok {
			continue // no package store for this ecosystem; it stays listed without a text
		}
		licenses, err := readTexts(dir, licenseStems)
		if err != nil {
			return sbom.Artifact{}, err
		}
		if len(licenses) == 0 {
			return sbom.Artifact{}, fmt.Errorf("no license file for %s %s (looked in %s for %s)",
				c.Name, c.Version, dir, strings.Join(licenseStems, ", "))
		}
		entry := sbom.ComponentTexts{License: intern(joinTexts(licenses))}
		notices, err := readTexts(dir, noticeStems)
		if err != nil {
			return sbom.Artifact{}, err
		}
		if len(notices) > 0 {
			entry.Notice = intern(joinTexts(notices))
		}
		artifact.Components[sbom.ComponentKey(c)] = entry
	}
	return artifact, nil
}

// componentDir is the directory a component's sources were unpacked into,
// and false for an ecosystem without a package store.
func componentDir(c sbom.Component, roots Roots) (string, bool, error) {
	switch c.Ecosystem {
	case "go":
		if c.Name == "std" {
			return roots.GoRoot, true, nil
		}
		// The module cache spells an uppercase letter as "!" and its
		// lowercase form, so case-insensitive filesystems cannot collide.
		escaped, err := module.EscapePath(c.Name)
		if err != nil {
			return "", false, fmt.Errorf("escape module path %s: %w", c.Name, err)
		}
		return filepath.Join(roots.GoModCache, escaped+"@"+c.Version), true, nil
	case "npm":
		dir, err := npmDir(roots.NodeModules, c.Name, c.Version)
		return dir, err == nil, err
	}
	return "", false, nil
}

// npmDir locates one version of an npm package. One version may be hoisted
// to the top, the rest nest under node_modules directories; package.json
// tells installed versions apart.
func npmDir(root, name, version string) (string, error) {
	relative := filepath.FromSlash(name)
	if hoisted := filepath.Join(root, relative); installedVersion(hoisted) == version {
		return hoisted, nil
	}
	nested := string(filepath.Separator) + filepath.Join("node_modules", relative)
	found := ""
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && strings.HasSuffix(path, nested) && installedVersion(path) == version {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("search %s for %s@%s: %w", root, name, version, err)
	}
	if found == "" {
		return "", fmt.Errorf("npm package %s@%s is not installed under %s; run pnpm install first", name, version, root)
	}
	return found, nil
}

// installedVersion is the version a package directory's package.json
// declares, or "".
func installedVersion(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(data, &manifest)
	return manifest.Version
}

// namedText is one license or notice file's name and normalized contents.
type namedText struct{ name, text string }

// readTexts returns every regular file in dir named for the first stem that
// matches anything, in filename order: a dual-licensed package owes both
// texts. Files with identical contents count once. A missing dir matches
// nothing.
func readTexts(dir string, stems []string) ([]namedText, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	for _, stem := range stems {
		var texts []namedText
		seen := map[string]bool{}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasPrefix(strings.ToLower(name), stem) || !isLicenseCandidate(name) {
				continue
			}
			path := filepath.Join(dir, name)
			info, err := os.Stat(path) // follows symlinks, unlike entry.Info
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if info.Size() > maxTextBytes {
				return nil, fmt.Errorf("%s is %d bytes, over the %d-byte cap; it is unlikely to be a license", path, info.Size(), maxTextBytes)
			}
			text, err := readText(path)
			if err != nil {
				return nil, err
			}
			if !seen[text] {
				seen[text] = true
				texts = append(texts, namedText{name, text})
			}
		}
		if len(texts) > 0 {
			return texts, nil
		}
	}
	return nil, nil
}

// readText reads one text file, refusing anything but UTF-8 and
// normalizing CRLF, so the pool hashes the same wherever it is generated.
func readText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("%s is not valid UTF-8", path)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n"), nil
}

// joinTexts renders a component's texts as one. A single text stays as it
// is; several are each headed by the file they came from.
func joinTexts(texts []namedText) string {
	if len(texts) == 1 {
		return texts[0].text
	}
	var out strings.Builder
	for i, t := range texts {
		if i > 0 {
			out.WriteString("\n")
		}
		out.WriteString(t.name + "\n" + strings.Repeat("-", len(t.name)) + "\n\n")
		out.WriteString(strings.TrimRight(t.text, "\n") + "\n")
	}
	return out.String()
}
