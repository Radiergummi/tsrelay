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

Every flag can also be set in the environment as `TSRELAY_<FLAG>`, with dashes as underscores (`TSRELAY_RELAY` for `--relay`, `TSRELAY_HOLD_TIMEOUT` for `--hold-timeout`); the command line wins. The rule is an explicit list, `envFlags` in `cmd/tsrelay/env.go`: the global flags (`--relay`, `--json`, `--no-color`, `--no-hyperlinks`), `attach --to`, the hook settings (`--attach-policy`, `--capture`, `--channel`, `--description`, `--ephemeral`, `--expires-in`, `--hold-timeout`, `--host-header`, `--idle-timeout`, `--max-body-bytes`, `--persistent`, `--rate-limit`, `--response-timeout`) and every `serve` flag. Flags that act rather than configure (`--all`, `--yes`, `--check`, `--force`, `--follow`, `--background`, `--public`, `--insecure`) and flags that name what a command works on (`--name`, `--path`) have no such variable. `TSRELAY_URL` and `TSRELAY_BACKGROUND_ID` are set by `tsrelay http` and the background supervisor for their children, so no flag may take those names.

## Output

On a terminal, output is colored, call lines are aligned, and each call ends with its ID as a clickable link to the inspector. Output that isn't a terminal is plain text, as is output with `NO_COLOR` set or `--no-color` given. If your terminal doesn't support hyperlinks, `--no-hyperlinks` (or `TSRELAY_NO_HYPERLINKS=1`) prints the full inspector URL instead.

`-j`/`--json` (on every command) prints JSON on stdout instead of text. `http` and `attach` print one event per line (`url`, `connected`, `disconnected`, `call`, `stopped`), and the other commands print one value. On a terminal the JSON is indented; piped, it's compact.

## Errors

Errors go to stderr and say what went wrong, why, and what to try instead:

```
✗ No target to forward calls to
  attach needs to know where your local app listens.

  Try:
    tsrelay attach stripe --to 3000
    tsrelay attach stripe --to http://localhost:8080
```

With `--json` they are `{"error": {"title": …, "reason": …, "fixes": […], "code": …}}`, where `code` is the relay's `Tsrelay-Error` code for a refusal (see the [error reference](errors.md)), `usage` for a mistake on the command line, or one of the CLI's own (such as `relay_unreachable`). A usage error exits with status 2, any other with 1.

## Open-source licenses

tsrelay is built from open-source projects whose licenses ask for their notices to travel with it. The web UI's footer links to the licenses page (`/licenses`), which lists each one with its license and notice texts, whatever roles you have. `tsrelay licenses` prints the same notices, and the relay's licenses page if it knows your relay. The relay also serves them as one text file at `/notices.txt`, and the software bill of materials they come from at `/sbom.cdx.json` (CycloneDX).
