---
title: Compatibility
description: What tsrelay promises not to break within a major version, what it doesn't promise, and what your clients and policy have to tolerate.
category: reference
tags: [compatibility, versioning, api, cli, grants]
---

# Compatibility

Within a major version, tsrelay only adds. What you build against 1.0 keeps working in 1.9: scripts, policy grants, relay configuration and receivers. This page lists what is promised, what isn't, and what your own code has to tolerate so that additions don't break it.

## What is promised

Within a major version, changes to these surfaces are additive only.

| Surface | What stays |
| --- | --- |
| HTTP API | Paths, request and response shapes, content negotiation and [problem codes](errors.md). |
| Policy grants | The app-capability JSON in your tailnet policy. A grant written for 1.0 means the same in 1.9. |
| Wire headers and statuses | The `Tsrelay-*` headers and trailers that senders and receivers see, and the 56x status codes. |
| Relay configuration | The flags and environment variables of `tsrelay serve`. |
| CLI | Command names, flags, environment variables, exit codes and machine-readable output (`--json`). |
| Tunnel protocol | The `tsrelay-tunnel/1` upgrade, its attach parameters, the `Tsrelay-Control` and `Tsrelay-Tunnel-*` headers and the control messages. Every v1 relay serves `tsrelay-tunnel/1`. The CLI removes every `Tsrelay-Tunnel-*` header before a call reaches your app, including ones it doesn't know. A `hook-updated` message always carries the hook's full current details, not only what changed. |

Human-readable text and layout can change in any release: the CLI's messages, tables and error explanations, and in the API the `title`, `detail` and `errors[].message` of problems, `replay.reason` and the value of `Tsrelay-Version`. Script against `--json`, codes and fields, not against text. The web UI's pages and their paths aren't promised either; hook URLs are.

## Defaults and limits

- **Defaults may change** in a minor release, such as a flag's default or a hook setting's: a release that changes one says so in its notes. To keep a value, set it explicitly.
- **Fixed limits only rise**: the 8 KiB path, the 280-character description, the 8 event streams per node, the 5 minutes an event stream stays open and the 64 events it may fall behind. Something accepted by 1.0 is accepted by every 1.x.

## What your code must tolerate

Additive means new things appear. A client that breaks on them breaks on the next minor release, so:

- **Ignore unknown fields** in API responses, `--json` output, hook settings and control messages.
- **Ignore unknown enumeration values, event-stream event types and control types.** Skip an event you don't know; don't close the stream because of it.
- **Fall back to the HTTP status for an unknown problem code.** The `code` field and the `Tsrelay-Error` header may name a code this page's [error reference](errors.md) doesn't have yet; the status says which class of failure it is.

The relay is strict the other way round, so a newer client can tell that a relay is too old for what it asked:

- A request field it doesn't know is refused with status 422, the code `unsupported_field`, and one entry in `errors` per unknown field, named by its dotted path such as `settings.rateLimit.burstiness`. Field names match exactly: `Path` isn't `path`.
- A write (`POST`, `PATCH`, `DELETE`) with a query parameter it doesn't take is refused the same way, the parameter named `query.<name>`. A read ignores parameters it doesn't know, as does a tunnel attach, so a newer client can pass optional ones.
- A yes-or-no parameter must be `true` or `false` (or `1`, `0` and the other spellings Go's `strconv.ParseBool` reads), given once; anything else is 400.

The CLI sends only the fields you set, so an old relay only sees the fields you use.

## Which relay features exist

`GET /` carries `features`, a list of names for optional API and tunnel capabilities that were added in a 1.x release. It is always present and empty at 1.0. A client that wants to use a later capability checks for its name first and degrades when it is missing.

The other way round, a client tells the relay what it supports with optional attach parameters, which a relay that doesn't know them ignores. A relay uses a capability that needs the client's help only when the client named it, so a 1.0 CLI is never sent something it can't act on.

## CLI and relay versions

Any v1.x CLI works against any v1.y relay, in both directions. A newer CLI against an older relay degrades cleanly when a feature is missing. The rule for a feature added in a 1.x release is that a CLI using it checks `features` first.

- The CLI warns about the relay's version only when the major versions differ. Development builds never warn.
- On the same major version, an older CLI may offer to update itself, at most now and then and only when interactive. A newer or equal CLI says nothing.
- `tsrelay update` refuses to install an older version than the one you run. Pass `--force` to downgrade on purpose.
- Every CLI request carries `User-Agent: tsrelay/<version> (<os>/<arch>)`, so a proxy or log in between can tell versions apart.

## Policy grants

Grants are read value by value, and what a relay doesn't understand it skips, so a newer policy keeps working against an older relay.

- An absent `channels` means `*`. An empty list, `[]`, matches nothing.
- Limits apply per user, and only values of the receiver count. A grant can only raise a limit above the relay's default. `maxHooks` and `maxTunnels` of `0` are accepted and change nothing. A grant's duration, `maxHookTtl`, must be positive: `"0s"` is invalid, and the relay skips that value.
- A value with a `requires` list names the features it needs. If any name is a feature this relay doesn't know, the relay skips the value and logs it. At 1.0 no features exist, so on a 1.0 relay any non-empty `requires` skips the value.
- An unknown role is logged and dropped. A value whose roles are all unknown is skipped.
- A malformed value is skipped and logged. The other values still apply.

## What zero means

Zero doesn't mean the same for every limit, and the meaning stays within a major version.

| Setting | Zero means |
| --- | --- |
| `--user-max-hook-ttl` | No limit. |
| `--user-max-hooks`, `--user-max-tunnels` | Nothing allowed: no hook or tunnel until a grant raises it. |
| The `--hook-<setting>-max` flags | No maximum. |
| `--hook-max-body-bytes-default` and the `maxBodyBytes` setting | No limit on the body size. |
| `--hook-hold-timeout-default` and the `holdTimeout` setting | No holding: a call without a tunnel fails at once. |
| `responseTimeout` and `idleTimeout` | Invalid; they must be greater than zero. |
| `--hook-hold-queue`, `--capture-calls`, `--capture-limit-bytes`, `--capture-total-bytes`, `--funnel-max-body-bytes`, `--tunnel-concurrency` | Refused at startup; they must be at least 1. |
| `--ephemeral-grace`, `--revalidate-interval`, `--shutdown-timeout` | Refused at startup; they must be greater than zero. |

## Shapes of lists and numbers

- Top-level lists are objects, so a list can gain siblings later: `{"hooks": [...]}`, `{"calls": [...]}`. The same holds for CLI output such as `{"runs": [...]}`. Lists of plain names inside an object, such as `features`, `tags` or `publicPatterns`, stay lists of strings.
- Timestamps are RFC 3339 strings in UTC, such as `2026-10-23T14:05:09.123Z`.
- Durations the relay measures are JSON numbers of milliseconds with microsecond precision: `durationMs`, `ttfbMs` and `waitedForMs`.
- Durations you configure, such as `holdTimeout` and `maxHookTtl`, are Go duration strings like `30s`.

## Exit codes

The CLI exits 0 on success, 1 on an error, 2 on a usage error, 3 when `http` or `attach` has no hook left to serve and 4 when `update --check` finds a newer relay version. With a command after `--`, `http` exits with the command's own code (128 plus the signal number if a signal killed it), and its own failures use 125, 126 and 127. The [exit code table](cli.md#exit-codes) has the details.

## What isn't promised

- **Rolling a relay back to an older minor version.** The hook store has a format version, and a relay refuses to start on a store in a newer format than it reads. A release changes the format only when it must, and says so in its notes; 1.0 writes the format 0.6.0 wrote. Take a copy of the state directory before you upgrade if you may want to go back.
- **Metrics.** A metric may be removed. A metric you keep using doesn't silently change what it means.
- **Log format.** This includes the [audit log](observability.md) lines. Parse them only against a relay version you have pinned.
- **State files.** `hooks.json`, `notices.json` and everything under `run/` are private to tsrelay.

A new major version may change anything above. An incompatible tunnel protocol gets a new `Upgrade` token, so a relay can serve both during a transition.
