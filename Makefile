# The web UI is built into web/dist, which the binary embeds. A plain
# `go build` works without Node and serves a placeholder page instead.
.PHONY: ui build dev check e2e sbom lint-docs lint-site

ui:
	cd web && pnpm install --frozen-lockfile && pnpm build

build: ui sbom
	go build -o tsrelay ./cmd/tsrelay

# Vite on :5173 in front of the dev harness (internal/server/uidev_test.go)
# on :8099. The harness is compiled to a binary and exec'd directly, not
# left running inside a `go test` wrapper: that wrapper's PID is what $$!
# would otherwise hold, and killing only the wrapper can orphan its child
# holding the ports. Interrupt to stop both.
dev:
	dir=$$(mktemp -d) && go test -c -o $$dir/uidev.test ./internal/server || exit 1; \
	(cd internal/server && TSRELAY_UI_DEV=1 exec $$dir/uidev.test -test.run '^TestUIDev$$' -test.count=1 -test.timeout=0) & \
	trap 'kill $$! 2>/dev/null; rm -rf "$$dir"' INT TERM EXIT; cd web && pnpm dev

check:
	go vet ./...
	@out="$$(gofmt -l $$(git ls-files '*.go'))"; if [ -n "$$out" ]; then echo "$$out"; echo "gofmt needs to be run on the files above"; exit 1; fi
	go test -race -p 2 ./...
	cd web && pnpm lint && pnpm fmt:check && pnpm typecheck && pnpm test && pnpm check:sinks

e2e: ui
	cd web && pnpm exec playwright install chromium && pnpm e2e

# The SBOM, license texts and notices internal/sbom embeds, written to
# internal/sbom/generated; see scripts/build-sbom.sh. Needs jq. Without
# them the binary has no licenses page or `tsrelay licenses`.
sbom:
	cd web && pnpm install --frozen-lockfile
	./scripts/build-sbom.sh

# Vale over the prose: the published docs and the pages a contributor reads
# first, then the site's own pages, whose copy lives in components rather
# than in docs/ and is checked as built (lint-site needs a website build).
DOCS := $(filter-out docs/DESIGN.md,$(wildcard docs/*.md)) README.md CONTRIBUTING.md SECURITY.md
SITE_PAGES := website/dist/index.html website/dist/download.html website/dist/404.html
VALE_FLAGS ?=

lint-docs: .vale/styles/.synced
	vale $(VALE_FLAGS) $(DOCS)

lint-site: .vale/styles/.synced
	vale $(VALE_FLAGS) $(SITE_PAGES)

# The style packages .vale.ini names, by URL and SHA-256. Fetched here rather
# than by `vale sync`, which checks nothing it downloads.
VALE_PACKAGES := \
	https://github.com/errata-ai/Google/releases/download/v0.7.0/Google.zip=dddb3dca035a067fe9ab13a1d8d8f89cb63e8b83ab4cf1934a935d60cd91d697 \
	https://github.com/errata-ai/proselint/releases/download/v0.3.4/proselint.zip=48b1fb481ba44f6505d44346b283f6cdc74efd234f913bbdb09bf34420fab81c

.vale/styles/.synced: Makefile
	@set -e; for package in $(VALE_PACKAGES); do \
		url=$${package%=*}; zip=$$(mktemp); \
		curl -fsSL -o "$$zip" "$$url"; \
		echo "$${package##*=}  $$zip" | sha256sum -c - >/dev/null || { echo "checksum mismatch: $$url" >&2; rm -f "$$zip"; exit 1; }; \
		unzip -qo "$$zip" -d .vale/styles; rm -f "$$zip"; \
	done
	touch $@
