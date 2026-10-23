---
title: Command line
description: Shell completion, environment variables, colored and JSON output, and how the tsrelay CLI reports errors.
category: guide
tags: [cli, completion, json, environment]
---

# Command line

## Completion

`tsrelay completion bash|zsh|fish|powershell` prints a completion script; for example `tsrelay completion fish > ~/.config/fish/completions/tsrelay.fish`. Besides commands and flags, it completes your hooks (by path and ID, for `attach` and `hooks show|update|delete`), channels for `--channel` (those your grants name, and those your hooks use), the targets `tsrelay http` remembered in this directory for `--to` and `<port|url>`, and background tunnels by ID and hook for `stop` and `logs`. Hooks and channels are asked of the relay live, giving up after 2 seconds; targets and background tunnels come from this machine. When anything fails, completion offers nothing and prints nothing.

## Environment

Every flag can also be set in the environment as `TSRELAY_<FLAG>`, with dashes as underscores (`TSRELAY_RELAY` for `--relay`, `TSRELAY_HOLD_TIMEOUT` for `--hold-timeout`); the command line wins. The rule is an explicit list, `envFlags` in `cmd/tsrelay/env.go`: the global flags (`--relay`, `--json`, `--no-color`, `--no-hyperlinks`), `attach --to`, the hook settings (`--attach-policy`, `--capture`, `--channel`, `--description`, `--ephemeral`, `--expires-in`, `--hold-timeout`, `--host-header`, `--idle-timeout`, `--max-body-bytes`, `--rate-limit`, `--response-timeout`) and every `serve` flag. Flags that act rather than configure (`--all`, `--yes`, `--check`, `--force`, `--follow`, `--background`, `--public`, `--insecure`) and flags that name what a command works on (`--path`) have no such variable. `TSRELAY_URL` and `TSRELAY_BACKGROUND_ID` are set by `tsrelay http` and the background supervisor for their children, so no flag may take those names.

## Output

On a terminal, output is colored, call lines are aligned, and each call ends with its ID as a clickable link to the inspector. Output that isn't a terminal is plain text, as is output with `NO_COLOR` set or `--no-color` given. If your terminal doesn't support hyperlinks, `--no-hyperlinks` (or `TSRELAY_NO_HYPERLINKS=1`) prints the full inspector URL instead.

`-j`/`--json` (on every command) prints JSON on stdout instead of text. `http` and `attach` print one event per line (see [Events](#events)), and the other commands print one object. On a terminal the JSON is indented; piped, it's compact. Consumers should ignore fields and event types they don't know: new ones are added without a new major version.

### JSON output

| Command | stdout |
| --- | --- |
| `hooks list` | `{"hooks": [hook, …]}` |
| `hooks show`, `create`, `update`, `rotate-url` | the hook |
| `hooks delete` | `{"deleted": [id, …], "failed": [{"ref": …, "error": {…}}, …]}`: `ref` is the hook as you named it; if any failed, the command exits 1 and the first error is also on stderr |
| `url` | `{"url": …}` |
| `ps` | `{"runs": [run, …]}` |
| `stop` | `{"stopped": [run, …], "failed": [{"id": …, "error": {…}}, …]}`; a stopped run's `state` is `stopped`, `killed` or `exited` (it had gone already) |
| `http`/`attach --background` | the run, once attached |
| `update` | `{"cliVersion": …, "relayVersion": …, "updated": …, "path": …}` |

A hook has the fields of the [API's hook](api.md). A run is a background tunnel: `{"id", "pid", "command", "relay", "target", "hooks": [{"id", "path", "url"}], "startedAt", "state"}`. `command` is the command line it runs, without the program; `state` (`running` or `exited`) is there for `ps` and `stop`.

Warnings and errors go to stderr, also one object each: `{"warning": …, "cliVersion": …, "relayVersion": …}` when the CLI and the relay are different major versions, and the error object described under [Errors](#errors). Nothing else is printed on stderr with `--json`.

### Events

`http` and `attach` print one object per line, each with an `event` and a `time` (UTC):

| `event` | When | Other fields |
| --- | --- | --- |
| `url` | a hook is served, or served again at a new URL | `hook` (ID), `url`, `publicUrl`, `recreated` |
| `connected`, `disconnected` | the tunnel attached, or was lost | `target`, `restored` |
| `call` | a call was delivered | `hook`, `method`, `path`, `status`, `aborted`, `durationMs`, `sender`, `callId`, `inspect` |
| `stopped` | a hook is no longer served (`hook`, and `reason`: `replaced`, `revoked` or `deleted`), or the command ends (`reason`: `interrupted`, `no-hooks`, `error` or `command-exited`, with `error` and `exitCode`) | |

A command started after `--` has its stdout sent to stderr, so that stdout carries only events.

## Errors

Errors go to stderr and say what went wrong, why, and what to try instead:

```
✗ No target to forward calls to
  attach needs to know where your local app listens.

  Try:
    tsrelay attach stripe --to 3000
    tsrelay attach stripe --to http://localhost:8080
```

With `--json` they are `{"error": {"title": …, "reason": …, "fixes": […], "code": …}}`, where `code` is the relay's `Tsrelay-Error` code for a refusal (see the [error reference](errors.md)), `usage` for a mistake on the command line, or one of the CLI's own (such as `relay_unreachable`).

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success. `http` and `attach` exit 0 when interrupted. |
| 1 | An error. |
| 2 | A usage error: tsrelay was called wrongly. |
| 3 | `http` or `attach` has no hook left to serve: the last one was deleted, its grants were revoked, or another tsrelay process took it over. |
| 4 | `update --check` found a newer relay version than yours. |

With a command after `--` (`tsrelay http 8080 -- npm run dev`), `http` exits with the command's own status (128 plus the signal number if a signal killed it, as shells report it), so tsrelay's failures there use the codes shells use for the same: **125** when tsrelay itself fails (any error, including no hook left), **126** when the command can't be run (not executable) and **127** when it isn't found. A usage error is still 2.

## Updating

`tsrelay update` replaces the tsrelay you run with the version of your relay; [Getting started](getting-started.md#updating-the-cli) has the details. A CLI works with any relay of the same major version, whichever is newer, so updating is only ever needed across major versions.

- `--check` only reports whether the versions differ. It exits 4 when the relay's version is newer than yours, and 0 when it is the same or older.
- `--force` installs the relay's version also when yours is newer. Without it, `update` refuses to downgrade.
- `--yes` skips the confirmation.

```bash
tsrelay update
tsrelay update --check
tsrelay update --force
```

## Open-source licenses

tsrelay is built from open-source projects whose licenses ask for their notices to travel with it. The web UI's footer links to the licenses page (`/licenses`), which lists each one with its license and notice texts, whatever roles you have. `tsrelay licenses` prints the same notices, and the relay's licenses page if it knows your relay. The relay also serves them as one text file at `/notices.txt`, and the software bill of materials they come from at `/sbom.cdx.json` (CycloneDX).
