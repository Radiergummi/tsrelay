---
name: run-tsrelay
description: Build, run, and drive tsrelay locally — start the dev relay with sample hooks and calls, click through or screenshot the web UI (hooks, inspector, admin; light/dark; as each identity), point the tsrelay CLI at it, and run the tests. Use when asked to run, start, launch, screenshot, or try the relay, its web UI, or its CLI, or to check a change in the running app rather than only in tests.
---

tsrelay needs a tailnet to run for real, so locally you drive the **dev
harness** instead: `internal/server/uidev_test.go`, one relay with sample
hooks, calls and a live tunnel, behind four ports that each stand for one
identity. Start it with `harness.sh`, drive its UI with `driver.mjs`
(headless Chromium via the Playwright in `web/node_modules`), and point
the CLI at it with `TSRELAY_RELAY`.

All paths are relative to the repository root. Verified on macOS (arm64).

## Prerequisites

Go (version in `go.mod`), Node (version in `web/.nvmrc`), pnpm.

```bash
cd web && pnpm install --frozen-lockfile && pnpm exec playwright install chromium && cd ..
```

## Build

The harness embeds `web/dist` at compile time. Rebuild it after any change
under `web/` (a missing `web/dist` serves a placeholder page):

```bash
make ui
```

## Run (agent path)

```bash
.claude/skills/run-tsrelay/harness.sh start   # compiles + starts, returns once :8099 answers
.claude/skills/run-tsrelay/harness.sh stop
```

Identities, one per port (the driver's `user` command switches between them):

| user | port | who | grants |
|---|---|---|---|
| `dev` | 8099 | dev@example.com | receiver + admin; owns `payments/done` (live tunnel), `idle`, `busy` (100 calls), `hostile` (XSS captures) |
| `admin` | 8098 | ops@example.com | admin only |
| `nobody` | 8097 | nobody@example.com | none |
| `other` | 8096 | other@example.com | receiver; owns `theirs` |

State is in memory: every `start` begins from the same seed.

### Drive the web UI

Pipe commands to the driver, one per line; it stops at the first failing one (exit 1):

```bash
node .claude/skills/run-tsrelay/driver.mjs <<'EOF'
nav /hooks
click text=payments/done
click text=201
wait text=12345678901234567890
theme dark
shot call-dark
user admin
nav /admin
shot admin
errors
EOF
```

A write flow — send a test request through the tunnel and see it arrive:

```bash
node .claude/skills/run-tsrelay/driver.mjs <<'EOF'
nav /hooks
click text=payments/done
click role=button[name="Send test request"]
fill internal:label="Path"s /from-skill
fill internal:label="Body"s {"via":"driver"}
click role=dialog >> role=button[name="Send"]
wait text=/from-skill
shot test-request
errors
EOF
```

Screenshots land in `$TMPDIR/tsrelay-shots/<name>.png` (override with
`TSRELAY_SHOTS`); the driver prints each path. Harness log:
`$TMPDIR/tsrelay-harness/harness.log`.

| command | does |
|---|---|
| `user dev\|admin\|nobody\|other` | switch identity; applies on the next `nav` |
| `theme light\|dark` | color scheme, at once |
| `nav <path>` | load a page on the current user's port, wait for network idle |
| `click <selector>` / `wait <selector>` | Playwright selector (`text=`, `role=…[name="…"]`, `internal:label="…"s`, css) |
| `fill <selector> <text>` | type into a field (fires React's onChange) |
| `press <key>` | e.g. `Enter`, `Escape` |
| `text [selector]` | print inner text (default `main`) — cheaper than a screenshot for checking content |
| `shot <name>` | full-page screenshot |
| `errors` | console errors, page errors, failed requests and 5xx so far |

### Drive the CLI

```bash
go build -o tsrelay ./cmd/tsrelay
TSRELAY_RELAY=http://127.0.0.1:8099 ./tsrelay hooks ls
```

Tunnels work too; use a throwaway state dir so you don't touch your real one:

```bash
export TSRELAY_RELAY=http://127.0.0.1:8099 TSRELAY_STATE_DIR=$(mktemp -d)
./tsrelay http 9123 --background   # prints the hook URL and a background id
./tsrelay ps
./tsrelay stop --all
```

## Run (human path)

`make dev` runs the same harness behind Vite with hot reload on
http://127.0.0.1:5173 (foreground; Ctrl-C stops both). Use it when editing
`web/` interactively — no `make ui` + restart per change.

## Test

```bash
go test ./cmd/tsrelay            # CLI, ~10 s
cd web && pnpm test              # 40 files / 197 tests, ~40 s
```

`make check` is the full CI set; `make e2e` runs the Playwright suite, which
starts this same harness itself.

## Gotchas

- **The ports are fixed and machine-wide.** Another checkout or worktree
  (`.claude/worktrees/`) may already run a harness, and Playwright's
  `reuseExistingServer` will happily run e2e against *yours* — you'll see
  calls like `/e2e-live` or `/arrived-light` appear. `harness.sh start`
  refuses when :8099 is taken; check `lsof -i:8099` before you `stop` it.
- **UI changes need `make ui` and a harness restart** — the Go binary embeds
  `web/dist` when it is compiled.
- **Nobody is a sender.** A plain `curl -X POST http://127.0.0.1:8099/~dev/payments/done`
  gets `403 sender_forbidden`. Make calls through the UI's *Send test request*
  (or the API it uses); the seeded calls cover the inspector.
- **`/` redirects to `/hooks`.**
- **Call bodies load after the call header.** Waiting for the *Replay* button
  is not enough — the body panel is still empty. `wait` for text from the body.
- **Form fields: use `internal:label="…"s`.** `role=textbox[name="Path"]`
  in the test-request dialog resolves to a non-input wrapper and `fill` fails.
- **The admin (`ops`) has no Hooks link**; go to `/admin` directly.

## Troubleshooting

- **`fill …: Element is not an <input>, <textarea>, <select> or [contenteditable]`**:
  the selector hit a label wrapper. Use `internal:label="Name"s`.
- **`wait text=…: Timeout 30000ms exceeded`**: the text isn't there — run
  `text` to see what the page renders (call IDs like `c_json` aren't shown in
  the list; status codes and paths are).
- **`:8099 is taken`** from `harness.sh start`: a harness is already running
  (yours or another checkout's). Reuse it, or `harness.sh stop` if it's stale.
