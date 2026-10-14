# Contributing to tsrelay

Thanks for helping. Bug reports, fixes and improvements are all welcome. For a larger change, open an issue first, so we can agree on the approach before you spend time on it.

Report security problems privately, as described in [SECURITY.md](SECURITY.md), not in an issue.

## Design

[docs/DESIGN.md](docs/DESIGN.md) describes what tsrelay does and why. Changes in behaviour should fit it, or update it in the same pull request.

tsrelay is meant to work for any tailnet. Don't hard-code names, tags, domains, ports or limits. Make them configurable, and give them a default that works without configuration.

## Development

You need Go (the version in `go.mod`). `node` is optional: with it, `go test ./internal/ui` also runs the web UI script's unit tests.

Before you send a pull request, run the same checks CI runs:

```bash
go vet ./...
gofmt -l .                                         # expect no output
go test -race ./...
go test -race -tags tailnet ./internal/server      # real tsnet nodes against an in-process control server
```

To try the web UI without a tailnet:

```bash
TSRELAY_UI_DEV=1 go test ./internal/server -run TestUIDev -timeout 0 -v   # then open http://127.0.0.1:8099
```

## Pull requests

- Add or update tests for what you change. Bug fixes come with a test that fails without the fix.
- For changes on the request path (relay, tunnel, inspector), compare benchmarks against `docs/perf/baseline.txt` with benchstat (see the README's Development section) and include the result.
- Keep each pull request to one change, and match the style of the code around it.
- Write commit messages as a plain imperative sentence that says what the change does, for example "Refuse replays of pending calls". No prefixes like `feat:`.
- tsrelay's direct dependencies are Tailscale, a few `golang.org/x` modules, OpenTelemetry (with the Prometheus client its exporter needs), and for the CLI cobra, fang and lipgloss (with the Charm modules they bring); anything else needs a good reason, and the web UI has no JavaScript or CSS dependencies.
- GitHub Actions are pinned to a full commit SHA, with the version in a comment. Only actions owned by GitHub and the Docker actions already in use are allowed to run.

## License

By contributing, you agree that your contributions are licensed under the [Apache License 2.0](LICENSE).
