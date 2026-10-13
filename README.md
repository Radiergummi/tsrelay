# tsrelay

Deliver HTTP callbacks from your tailnet to apps running on your laptop, without letting production connect to developer machines and without exposing anything publicly.

Production services call the relay, which is its own tailnet node. Your laptop dials *out* to the relay and attaches a tunnel. The relay forwards each callback down the tunnel to your local app and returns your app's response. Access is controlled entirely by your tailnet policy.

```
prod service ──HTTPS──▶ tsrelay (tailnet node) ◀──tunnel── tsrelay http 8080 ──▶ localhost:8080
```

tsrelay is deliberately narrow. It forwards only to hooks you create explicitly. It never intercepts services as Telepresence or mirrord do, and it is never reachable from the internet (it never uses Funnel).

## Quick start

### 1. Grant access in your tailnet policy

```jsonc
"tagOwners": { "tag:tsrelay": ["autogroup:admin"] },
"grants": [
  // Who may receive callbacks
  { "src": ["autogroup:member"], "dst": ["tag:tsrelay"], "ip": ["tcp:443"],
    "app": { "mazetti.me/cap/relay": [{ "roles": ["receiver"] }] } },
  // Who may send callbacks
  { "src": ["tag:prod-worker"], "dst": ["tag:tsrelay"], "ip": ["tcp:443"],
    "app": { "mazetti.me/cap/relay": [{ "roles": ["sender"] }] } }
]
```

Enable [HTTPS certificates](https://tailscale.com/kb/1153/enabling-https) for your tailnet, or run the relay with `--tls=false` and grant `tcp:80` instead.

### 2. Run the relay

```bash
TS_CLIENT_SECRET=tskey-client-… tsrelay serve
```

Use an OAuth client secret with the `tag:tsrelay` tag. Auth keys (`TS_AUTHKEY`) also work, but they expire. State lives in `--state-dir`: `/var/lib/tsrelay` in containers, `~/.local/state/tsrelay` otherwise. Run exactly one relay per state directory.

### 3. Forward a hook to your app

```bash
$ tsrelay http 8080
https://tsrelay.example.ts.net/~moritz/amber-otter-lamp
Forwarding https://tsrelay.example.ts.net/~moritz/amber-otter-lamp → http://localhost:8080
14:02:11 POST /payments/done → 200  12ms  tag:prod-worker  https://tsrelay.example.ts.net/inspect/c_7f3a
```

Configure that URL as the callback URL in the app that calls you. Paths below it are passed on: `…/amber-otter-lamp/payments/done` reaches your app as `/payments/done`. Running `tsrelay http 8080` again in the same directory prints the same URL, so you can keep it in your `.env`.

Useful flags:
- `--name payments/v1` picks the path yourself.
- `--persistent` keeps the hook after the command exits. While no tunnel is attached, callers get `503` with `Retry-After`.
- `--relay host` points at a relay that isn't at `tsrelay.<your tailnet>`.

## Channels

Grants can scope senders to receivers with channel patterns (`*` matches anything):

```jsonc
{ "roles": ["receiver"], "channels": ["staging", "prod.*"] }   // may create hooks in these channels
{ "roles": ["sender"],   "channels": ["staging"] }             // may only call staging hooks
```

Hooks are created in channel `default` unless you choose another one with `--channel`. A grant without `channels` means `["*"]`. Scope sender grants first, because they are the broad ones.

## Hook settings

Every hook has delivery settings, each with a relay-wide default and maximum (`--hook-<setting>-default` / `--hook-<setting>-max`, above):

| Setting | Default | |
|---|---|---|
| `attachPolicy` | `takeover` | `reject` fails a second tunnel's attach with `409` instead of replacing the first |
| `hostHeader` | `preserve` | `rewrite` sets `Host` to your local target's host instead of the relay's |
| `holdTimeout` | `30s` | How long a call waits for a tunnel to attach; `0` disables holding |
| `responseTimeout` | `2m` | How long to wait for your app's response headers |
| `idleTimeout` | `60s` | Longest gap in body traffic, either direction, before the call fails |
| `maxBodyBytes` | unlimited | Request body size limit |
| `rateLimit` | `100/200` | Calls per second and burst, per hook |
| `capture` | `true` | Keep request and response bodies for the inspector; metadata and headers are always recorded |

Set them when creating or updating a persistent hook:

```bash
tsrelay hooks create payments/v1 --rate-limit 10/20 --host-header rewrite
tsrelay hooks update payments/v1 --hold-timeout 0s
```

`tsrelay http` always uses the relay defaults; use `tsrelay hooks create` plus `tsrelay attach` for a hook with custom settings, such as `--capture=false` to stop keeping bodies for just that hook.

## Managing hooks

```bash
tsrelay hooks ls [--json]                            # your hooks
tsrelay hooks create [<path>] [flags]                # create a persistent hook
tsrelay hooks update <id|path> [flags]               # change one (only the given flags)
tsrelay hooks rm <id|path>...                         # delete one or more
tsrelay url [<port|url>]                              # print the URL tsrelay http would use, without contacting the relay
tsrelay attach <id|path>... --forward-to <port|url>   # serve existing hooks from one local target
```

`tsrelay http` and `tsrelay attach` both accept:
- `--insecure` to skip TLS certificate verification when the target is `https://` (self-signed local certificates).
- `--json` to print one JSON event per line on stdout (`url`, `connected`, `disconnected`, `call`, `stopped`) instead of text; useful for scripting.

If a served hook's path is changed (`tsrelay hooks update` or the API), both print its new URL on stdout, and `tsrelay http` remembers it.

`tsrelay http` additionally accepts `-- cmd args...` to start a local process with `TSRELAY_URL` set in its environment, and stop it (SIGTERM, then a kill after 5s) when the tunnel ends:
```bash
tsrelay http 8080 -- npm run dev
```
The command keeps the `TSRELAY_URL` it was started with, even if the hook's path changes while it runs.

## What your app receives

Bodies arrive exactly as sent, byte for byte, so signatures from Stripe, GitHub, Svix, or [Standard Webhooks](https://www.standardwebhooks.com/) verify normally. The relay adds these headers, after removing any copies the sender sent:

| Header | Value |
|---|---|
| `Tsrelay-Sender-Node` | The calling machine's name |
| `Tsrelay-Sender-Tags` | Its tags, comma-separated |
| `Tsrelay-Sender-Login` | Its user, if it is not a tagged machine |
| `Tsrelay-Hook` | `user/path` of the hook |
| `Tsrelay-Call-Id` | Also returned to the caller, for matching logs |
| `X-Forwarded-For`, `-Host`, `-Proto`, `-Prefix` | The original request, for URL-signing schemes |

## Web UI

Open the relay's URL (`https://tsrelay.<your tailnet>/`) in a browser on your tailnet. You see your hooks with their tunnels and latest calls, and can create, edit and delete hooks. Each hook's page shows its calls as they arrive, and lets you send it a test request. A call's page shows the request and the response side by side, and lets you replay the call or copy it as a curl command (aimed at your local target or at the hook URL). Admins also get an admin page listing every hook, as metadata only. Each call line `tsrelay http` prints ends with a link to that call.

The UI needs JavaScript to make changes; without it, it is read-only. To try the UI locally without a tailnet:

```bash
TSRELAY_UI_DEV=1 go test ./internal/server -run TestUIDev -timeout 0 -v   # then open http://127.0.0.1:8099
```

## Inspecting calls

The relay keeps the last 100 calls of each hook in memory (`--capture-calls`): method, path, sender, status and timings, the query and the headers without the redacted ones (`--redact-headers`), each capped at `--capture-limit` bytes, and the first 64 KiB of each body (`--capture-limit`). Only the hook's owner sees queries, headers and bodies; admins see the rest. Calls are lost when the relay restarts.

| | |
|---|---|
| `GET /api/v1/hooks/{id}/calls` | Call summaries, newest first |
| `GET /api/v1/hooks/{id}/calls/{callID}` | One call with its captured headers and bodies |
| `POST /api/v1/hooks/{id}/calls/{callID}/replay` | Send the call again; its body and query string must have been captured in full |
| `POST /api/v1/hooks/{id}/test` | Send `{"method","path","headers","body"}` through the relay to your app |
| `GET /api/v1/events` | Server-sent events for your calls and hooks |

Replays and test requests go through the whole relay to your app. The answer is recorded in the inspector and returned by the API, never sent to the original sender. Every response a sender gets carries `Tsrelay-Call-Id`, which names the call here.

## Errors

Errors produced by the relay itself are `application/problem+json` responses and carry a `Tsrelay-Error` header. Responses from your app are never marked, including its own 5xx.

| `Tsrelay-Error` | Status | Meaning |
|---|---|---|
| `sender_forbidden` | 403 | The caller has no sender grant |
| `hook_not_found` | 404 | No such hook, not in the caller's channels, or it was deleted/expired while a call was held for it |
| `body_too_large` | 413 | Over the hook's `maxBodyBytes` (unlimited by default) |
| `rate_limited` | 429 | Over the hook's `rateLimit` (100/s, burst 200 by default); `Retry-After` is the limiter's wait, rounded up to whole seconds |
| `tunnel_unavailable` | 503 | No tunnel attached within `holdTimeout` (30s by default), or more than `--hold-queue` calls are already waiting; `Retry-After` is `holdTimeout`, but never less than 5s |
| `tunnel_overloaded` | 503 | Too many concurrent calls on the tunnel; `Retry-After: 1` |
| `upstream_unreachable` | 502 | The tunnel is up but your app is not listening |
| `tunnel_lost` | 502 | The tunnel dropped mid-call |
| `upstream_timeout` | 504 | Your app did not answer within the hook's `responseTimeout`, or a response stalled past `idleTimeout` |

## Relay configuration

Every flag can also be set as an environment variable: `--ephemeral-grace` becomes `TSRELAY_EPHEMERAL_GRACE`. Run `tsrelay serve --help` for the full list. The most relevant flags:

| Flag | Default | |
|---|---|---|
| `--hostname` | `tsrelay` | Tailnet machine name |
| `--tls` | `true` | HTTPS on 443; `false` serves HTTP on 80 |
| `--advertise-tags` | `tag:tsrelay` | |
| `--cap-name` | `mazetti.me/cap/relay` | Capability that carries grants |
| `--user-slug` | `local-part` | `moritz@example.com` → `~moritz`; `login` uses the full login |
| `--user-slug-override` | | `alice@b.com=alice-b`, for collisions |
| `--max-hooks-default` / `--max-tunnels-default` | `10` / `5` | Per user; grants can raise them with `maxHooks` / `maxTunnels` |
| `--max-hook-ttl-default` | unlimited | Longest a persistent hook may live; grants can raise it with `maxHookTTL` |
| `--ephemeral-grace` | `10m` | How long a hook survives without its tunnel |
| `--tunnel-concurrency` | `32` | Concurrent calls per tunnel |
| `--hold-queue` | `100` | Calls per hook that may wait for a tunnel to attach at once |
| `--revalidate-interval` | `60s` | How often an attached tunnel's grant is re-checked |
| `--capture-calls` | `100` | Calls per hook the inspector keeps in memory |
| `--capture-limit` | `65536` | Bytes the inspector keeps of each body, of each call's headers, and of its query string |
| `--capture-bodies` | `true` | `false` keeps no bodies for any hook |
| `--redact-headers` | `Authorization,Proxy-Authorization,Cookie,Set-Cookie,X-Api-Key` | Headers whose values are never kept; an empty value keeps these defaults, so redaction cannot be turned off |
| `--ops-listen` | off | Host address (e.g. `:9090`) for `/healthz`, `/readyz` and `/metrics`; never on the tailnet |

Every numeric or duration setting from [Hook settings](#hook-settings) also has a relay-wide default and maximum flag, named `--hook-<setting>-default` and `--hook-<setting>-max`, for example `--hook-rate-limit-default=100/200`, or `--hook-max-body-bytes-default=1048576 --hook-max-body-bytes-max=10485760` (the default body size is unlimited, which a maximum would not allow, so set both); `attachPolicy`, `hostHeader` and `capture` only get a `-default` flag, since there's no sensible maximum for them. A hook may only set a value within the relay's maximum. `--response-timeout` still works as a deprecated alias of `--hook-response-timeout-default`.

The audit log is JSON on stderr. It never contains bodies, query strings, or header values.

## Manual smoke test (TLS)

The automated tests cover everything except real certificates. Before a release:

1. On a tailnet with HTTPS enabled, run `tsrelay serve` with an OAuth client secret. Expect `relay ready` with an `https://` URL.
2. On a laptop with the receiver grant, run `tsrelay http 8080` against `python3 -m http.server 8080`. Expect the URL on stdout.
3. From a node with the sender grant, run `curl -i <url>/`. Expect `200` and a line in the CLI.
4. From a node without grants, run `curl -i <url>/`. Expect `403 sender_forbidden`.
5. Run the relay with `--tls=false` and repeat steps 2–3 with an `http://` relay URL.

## Development

```bash
go vet ./...
gofmt -l .                                         # expect no output
go test -race ./...                                # everything except the tailnet test
go test -race -tags tailnet ./internal/server      # real tsnet nodes against an in-process control server
```

The UI script's unit tests run with `node --test` as part of `go test ./internal/ui` when `node` is installed, and are skipped otherwise.

`.github/workflows/ci.yml` runs the same four commands on every push and pull request. `.github/workflows/release.yml` builds binaries and a container image for tags matching `v*`; `tsrelay version` reports the version baked in at build time (`dev` for a local build).

The design is in [docs/DESIGN.md](docs/DESIGN.md).

## Metrics and deployment

With `--ops-listen=:9090`, the relay serves `/healthz` (process alive), `/readyz` (tailnet joined and the hook store loaded) and `/metrics` (Prometheus text format: `tsrelay_calls_total`, `tsrelay_call_bytes_total`, `tsrelay_tunnels`, `tsrelay_hooks`, `tsrelay_held_calls`, `tsrelay_authz_denials_total`) — never on the tailnet interface.

Ship it as:
- a static binary (`go build ./cmd/tsrelay`, or download one from a [release](../../releases))
- a container image, `ghcr.io/radiergummi/tsrelay` (see the `Dockerfile`)
- Kubernetes, with `deploy/kubernetes/` (a `StatefulSet` with `replicas: 1`, a `Secret` example, no `Service` — the relay is reached over the tailnet only)
- Docker Compose, with `deploy/compose.yaml`

Run exactly one relay per state directory (`--state-dir`); see DESIGN.md §11 and §14.

## License

Apache License 2.0; see [LICENSE](LICENSE) and [NOTICE](NOTICE).
