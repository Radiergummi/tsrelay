#!/usr/bin/env bash
# Generates the CycloneDX SBOM that internal/sbom embeds: the Go modules the
# tsrelay binary links (cyclonedx-gomod app) and the web UI's production npm
# dependencies (scripts/sbomnpm). The two are merged with jq into an envelope
# without volatile fields (serial number, timestamp, version), so the file
# changes only when dependencies do. Then scripts/licensetexts harvests the
# license and notice texts. Needs Go, jq, and web/node_modules (pnpm install).
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
out=internal/sbom/sbom.cdx.json
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

command -v jq >/dev/null || { echo "error: jq not found; install it from https://jqlang.org" >&2; exit 1; }
[ -d web/node_modules/.pnpm ] || { echo "error: web/node_modules is missing; run pnpm install --frozen-lockfile in web/" >&2; exit 1; }

# The standard library's version is the toolchain's, so the toolchain is
# pinned to the one go.mod names.
export GOTOOLCHAIN="go$(go mod edit -json | jq -r .Go)"

echo "==> Go modules"
# cyclonedx-gomod writes GOOS/GOARCH into every purl and picks
# platform-specific imports, so it is pinned to linux/amd64 (the Docker
# image) to give the same output on every machine. `go tool` would
# cross-compile the tool itself for that target, so it is built for the host
# from the version go.mod's tool directive pins, without touching go.mod.
go build -mod=readonly -o "$tmp/cyclonedx-gomod" github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod
# It also reads the main module's version with go-git, which cannot open a
# git worktree, so it runs on a copy of the source with a plain clone as its
# .git. That version is dropped in the merge below.
mkdir "$tmp/src"
git ls-files -z -co --exclude-standard | tar -c --null -T - -f - | tar -x -C "$tmp/src" -f -
git clone --quiet --bare . "$tmp/src/.git"
(cd "$tmp/src" && GOOS=linux GOARCH=amd64 GOFLAGS=-buildvcs=false "$tmp/cyclonedx-gomod" app -main cmd/tsrelay -json -licenses -std -output "$tmp/go.cdx.json" .)

echo "==> npm packages (production only)"
go run ./scripts/sbomnpm -out "$tmp/npm.cdx.json"
# The lockfile is the only record of the tarball hashes; losing them fails.
grep -q 'SHA-512' "$tmp/npm.cdx.json" || { echo "error: npm components have no integrity hashes; is web/pnpm-lock.yaml current?" >&2; exit 1; }

echo "==> merge"
# License detection finds the standard library's only where GOROOT holds a
# LICENSE, which Homebrew's does not; it is BSD-3-Clause everywhere.
jq -s '{
  bomFormat: "CycloneDX",
  specVersion: (.[0].specVersion // "1.6"),
  version: 1,
  metadata: { component: { type: "application", name: "tsrelay" } },
  components: (
    ((.[0].components // []) | map(if .name == "std" then del(.evidence) + {licenses: [{license: {id: "BSD-3-Clause"}}]} else . end))
    + (.[1].components // [])
  )
}' "$tmp/go.cdx.json" "$tmp/npm.cdx.json" > "$out"

echo "==> license and notice texts"
go run ./scripts/licensetexts
echo "wrote $out"
