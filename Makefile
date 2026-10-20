# The web UI is built into web/dist, which the binary embeds. A plain
# `go build` works without Node and serves a placeholder page instead.
.PHONY: ui build dev check e2e sbom

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
