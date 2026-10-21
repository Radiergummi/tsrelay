# Contributing to tsrelay

Thanks for helping. Bug reports, fixes and improvements are all welcome. For a larger change, open an issue first, so we can agree on the approach before you spend time on it.

Report security problems privately, as described in [SECURITY.md](SECURITY.md), not in an issue.

## Design

[docs/DESIGN.md](docs/DESIGN.md) describes what tsrelay does and why. Changes in behaviour should fit it, or update it in the same pull request.

tsrelay is meant to work for any tailnet. Don't hard-code names, tags, domains, ports or limits. Make them configurable, and give them a default that works without configuration.

## Development

You need Go (the version in `go.mod`) and, for the web UI, Node (the version in `web/.nvmrc`) with pnpm (any recent pnpm switches itself to the version in `web/package.json`).

Before you send a pull request, run the same checks CI runs:

```bash
make check                                         # Go and web UI checks
make e2e                                           # the web UI's end-to-end tests
go test -race -tags tailnet ./internal/server      # real tsnet nodes against an in-process control server
```

Other useful targets:

```bash
make ui        # build the web UI into web/dist, which the binary embeds
make build     # make ui and make sbom, then build ./tsrelay
make sbom      # build the SBOM and license texts into internal/sbom/generated (needs jq)
```

The binary embeds a software bill of materials, its components' license and notice texts, and the notices built from them (`internal/sbom`), which the licenses page and `tsrelay licenses` show. They are built, not committed: `make build` builds them, and CI builds them for the release. The output is the same on every machine: the Go part is pinned to linux/amd64 and to the Go version go.mod names.

A plain `go build` works without Node, pnpm and jq; its binary serves a placeholder page instead of the UI, and has no licenses page or `tsrelay licenses`.

To work on the web UI, `make dev` serves it with hot reload on http://127.0.0.1:5173 against a dev relay with sample data. That relay also listens directly on 127.0.0.1:8098 (admin only), :8097 (no grant) and :8096 (another user), so you can open those to see the other identities' views.

The website at https://tsrelay.mazetti.me lives in `website/` and builds its pages from the Markdown in `docs/` (except `DESIGN.md`). `cd website && pnpm install && pnpm dev` serves it on http://localhost:4321; `pnpm build` needs a clone with full history, because every page is dated by its last commit, and access to GitHub's API, from which the download page reads the latest release. When you add an error code to `internal/problem`, document it in `docs/errors.md`: each code's problem `type` links to its entry there, and the package's tests fail until it has one.

## Performance

```bash
go test -run='^$' -bench=. -count=6 -p 1 ./internal/server ./internal/tunnel/... ./internal/inspect > new.txt
go run golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e docs/perf/baseline.txt new.txt
TSRELAY_LOAD=1 go test ./internal/server -run TestLoad -count=1 -v      # add -cpuprofile cpu.out to profile
```

`TestLoad` drives the relay in-process (knobs: `TSRELAY_LOAD_CONCURRENCY`, `_DURATION`, `_BODY`, `_MODE=plain|streaming`) and reports throughput and the latency the relay adds over calling the app directly.

`.github/workflows/ci.yml` runs the checks, the UI build, and the end-to-end tests on every push and pull request.

## Pull requests

- Add or update tests for what you change. Bug fixes come with a test that fails without the fix.
- For changes on the request path (relay, tunnel, inspector), compare benchmarks against `docs/perf/baseline.txt` with benchstat (see [Performance](#performance)) and include the result.
- Keep each pull request to one change, and match the style of the code around it.
- Write commit messages as a plain imperative sentence that says what the change does, for example "Refuse replays of pending calls". No prefixes like `feat:`.
- tsrelay's direct dependencies are Tailscale, a few `golang.org/x` modules, OpenTelemetry (with the Prometheus client its exporter needs), `go.yaml.in/yaml` (to serve the OpenAPI document as JSON), and for the CLI cobra, fang and lipgloss (with the Charm modules they bring); anything else needs a good reason. The web UI is a React and Tailwind app under `web/`, whose dependencies are listed in `web/package.json`; new ones need a good reason too, and anything that injects inline scripts or styles is ruled out by the UI's CSP.
- GitHub Actions are pinned to a full commit SHA, with the version in a comment. Only actions owned by GitHub and the Docker actions already in use are allowed to run.

## Releases

Pushing a `v*` tag runs `.github/workflows/release.yml`. It first runs CI on the tagged commit, and builds nothing unless that passes. The macOS binaries are signed with a Developer ID Application certificate and notarized with [rcodesign](https://github.com/indygreg/apple-platform-rs/tree/main/apple-codesign). Their secrets live in a GitHub environment named `release`, limited to `v*` tags, so only the signing job can read them:

- `MACOS_P12`: the certificate and its private key, exported from Keychain Access as a .p12, base64-encoded (`base64 -i cert.p12 | pbcopy`).
- `MACOS_P12_PASSWORD`: the password the .p12 was exported with.
- `ASC_API_KEY_JSON`: an App Store Connect API key with the Developer role, encoded with `rcodesign encode-app-store-connect-api-key <issuer-id> <key-id> AuthKey_<key-id>.p8`.

`tsrelay --version` reports the version baked in at build time (`dev` for a local build).

### Manual smoke test (TLS)

The automated tests cover everything except real certificates. Before a release:

1. On a tailnet with HTTPS enabled, run `tsrelay serve` with an OAuth client secret. Expect `relay ready` with an `https://` URL.
2. On a laptop with the receiver grant, run `tsrelay http 8080` against `python3 -m http.server 8080`. Expect the URL on stdout.
3. From a node with the sender grant, run `curl -i <url>/`. Expect `200` and a line in the CLI.
4. From a node without grants, run `curl -i <url>/`. Expect `403 sender_forbidden`.
5. Run the relay with `--tls=false` and repeat steps 2–3 with an `http://` relay URL.

## License

By contributing, you agree that your contributions are licensed under the [Apache License 2.0](LICENSE).
