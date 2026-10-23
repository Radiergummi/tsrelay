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
| Tunnel protocol | The `tsrelay-tunnel/1` upgrade, its attach parameters, the `Tsrelay-Control` and `Tsrelay-Tunnel-*` headers and the control messages. Every v1 relay serves `tsrelay-tunnel/1`. |

Human-readable text and layout, such as the CLI's messages, tables and error explanations, can change in any release. Script against `--json`, not against text.

## What your code must tolerate

Additive means new things appear. A client that breaks on them breaks on the next minor release, so:

- **Ignore unknown fields** in API responses, `--json` output, hook settings and control messages.
- **Ignore unknown enumeration values, event-stream event types and control types.** Skip an event you don't know; don't close the stream because of it.
- **Fall back to the HTTP status for an unknown problem code.** The `code` field and the `Tsrelay-Error` header may name a code this page's [error reference](errors.md) doesn't have yet; the status says which class of failure it is.

The relay is strict the other way round. It refuses a request field it doesn't know with status 422, the code `unsupported_field`, and one entry in `errors` per unknown field, named by its dotted path such as `settings.rateLimit.burstiness`, so a newer client can tell that a relay is too old for what it asked. The CLI sends only the fields you set, so an old relay only sees the fields you use.

## Which relay features exist

`GET /` carries `features`, a list of names for optional API and tunnel capabilities that were added in a 1.x release. It is always present and empty at 1.0. A client that wants to use a later capability checks for its name first and degrades when it is missing.

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
| `--hook-hold-queue`, `--capture-calls`, `--capture-limit-bytes`, `--funnel-max-body-bytes`, `--tunnel-concurrency` | Refused at startup; they must be at least 1. |
| `--ephemeral-grace`, `--revalidate-interval`, `--shutdown-timeout` | Refused at startup; they must be greater than zero. |

## Shapes of lists and numbers

- Lists are objects, so a list can gain siblings later: `{"hooks": [...]}`, `{"calls": [...]}`. The same holds for CLI output such as `{"runs": [...]}`.
- Durations the relay measures are JSON numbers of milliseconds with microsecond precision: `durationMs`, `ttfbMs` and `waitedForMs`.
- Durations you configure, such as `holdTimeout` and `maxHookTtl`, are Go duration strings like `30s`.

## Exit codes

The CLI exits 0 on success, 1 on an error, 2 on a usage error, 3 when `http` or `attach` has no hook left to serve and 4 when `update --check` finds a newer relay version. With a command after `--`, `http` exits with the command's own code, and its own failures use 125, 126 and 127. The [exit code table](cli.md#exit-codes) has the details.

## What isn't promised

- **Rolling a relay back to an older minor version.** An older relay refuses to start on a store written by a newer one. Take a copy of the state directory before you upgrade if you may want to go back.
- **Metrics.** A metric may be removed. A metric you keep using doesn't silently change what it means.
- **Log format.** This includes the [audit log](observability.md) lines. Parse them only against a relay version you have pinned.
- **State files.** `hooks.json`, `notices.json` and everything under `run/` are private to tsrelay.

A new major version may change anything above. An incompatible tunnel protocol gets a new `Upgrade` token, so a relay can serve both during a transition.
