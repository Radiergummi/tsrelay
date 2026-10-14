# The web UI is built into web/dist, which the binary embeds. A plain
# `go build` works without Node and serves a placeholder page instead.
.PHONY: ui build dev check e2e

ui:
	cd web && pnpm install --frozen-lockfile && pnpm build

build: ui
	go build -o tsrelay ./cmd/tsrelay

# Vite on :5173 in front of the dev harness (internal/server/uidev_test.go)
# on :8099. Interrupt to stop both.
dev:
	TSRELAY_UI_DEV=1 go test ./internal/server -run '^TestUIDev$$' -count=1 -timeout 0 & \
	trap 'kill $$!' EXIT; cd web && pnpm dev

check:
	go vet ./...
	@out="$$(gofmt -l $$(git ls-files '*.go'))"; if [ -n "$$out" ]; then echo "$$out"; echo "gofmt needs to be run on the files above"; exit 1; fi
	go test -race -p 2 ./...
	cd web && pnpm lint && pnpm fmt:check && pnpm typecheck && pnpm test && pnpm check:sinks

e2e: ui
	cd web && pnpm exec playwright install chromium && pnpm e2e
