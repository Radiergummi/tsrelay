# tsrelay

Deliver HTTP callbacks from your tailnet to apps running locally, without letting production connect to developer machines, and with nothing exposed publicly unless you opt in.

Production services call the relay, which is its own tailnet node. Developer devices dial *out* to the relay and attach a tunnel. The relay forwards each callback down the tunnel to your local app and returns your app's response. Access is controlled entirely by your tailnet policy.

<img width="936" height="648" alt="Graph depicting the call chain between a tailnet service and a local device" src="https://github.com/user-attachments/assets/dbacfb85-0221-4700-a9ac-2bf28610b504" />

tsrelay is deliberately narrow. It forwards only to hooks you create explicitly. It never intercepts services as Telepresence or mirrord do, and it is reachable from the internet only for hooks their owners explicitly make public, through Tailscale Funnel, when the operator runs it with `--funnel`.

## Why

Communication from a local device and a tailnet service is easy: You grant a user group permission to connect to a tag, and it works.
The reverse, however, is more complicated: While you can obviously just create the reverse policy and allow a tag to connect to all devices of a user, or a whole user group, that opens up a lot of security issues, is not auditable, and grants any service in prod a permanent backdoor into your endpoints.
It also encodes details about your local developer's environments into your ACL policy, which is annoying to maintain and keep track of.  
To avoid all of this, you could reach for a tool like [ngrok](https://ngrok.com/) or [Cloudflare Tunnel](https://developers.cloudflare.com/tunnel/), but that tunnels internal traffic via the internet.

Instead, you can also set up tsrelay: It adds a tiny relay service to your tailnet that can be configured using standard ACLs and [app capabilities](https://tailscale.com/docs/features/access-control/grants/grants-app-capabilities) to control who can send and receive callbacks in your tailnet.
Users can set up hooks that are fully configurable, can be routed to multiple user devices, buffer requests on unreliable lines, have a full request log, include Tailscale authorization headers, and more. In turn, the relay service itself has a full audit log, OpenTelemetry traces and metrics, as well as global control over all hooks.

A web interface lets users manage their hooks and view the request log, which also offers manual calls, replays, and access to all details. Everything is updated in realtime.

**Contents**

- [Using tsrelay](#using-tsrelay): for developers whose tailnet already runs a relay
  - [Get started](#get-started) · [Hooks](#hooks) · [Public hooks](#public-hooks) · [Channels](#channels) · [Managing hooks](#managing-hooks) · [Hook settings](#hook-settings) · [What your app receives](#what-your-app-receives) · [Web UI](#web-ui) · [Inspecting calls](#inspecting-calls) · [The API](#the-api) · [Errors](#errors) · [Completion and output](#completion-and-output)
- [Running a relay](#running-a-relay): for operators
  - [Tailnet policy and roles](#tailnet-policy-and-roles) · [Making someone an admin](#making-someone-an-admin) · [Run the relay](#run-the-relay) · [Serving public hooks](#serving-public-hooks) · [Configuration](#configuration) · [Hook defaults and limits](#hook-defaults-and-limits) · [Deployment](#deployment) · [Metrics](#metrics) · [Tracing](#tracing) · [Profiling](#profiling) · [Verifying a download](#verifying-a-download)
- [Contributing](#contributing)

# Using tsrelay

This half is for you if someone on your tailnet already runs a relay and has granted you access. To set up a relay, see [Running a relay](#running-a-relay).

## Get started

Install the CLI from your relay: open its web UI and choose **Get the CLI**, or run

```bash
curl -fsSL https://tsrelay.<your tailnet>/install.sh | sh        # macOS, Linux
irm https://tsrelay.<your tailnet>/install.ps1 | iex             # Windows (PowerShell)
```

You get the version your relay runs. When the two drift apart, the CLI warns and prints the command to update.

Then forward a hook to your local app:

```bash
$ tsrelay http 8080
tsrelay  forwarding to http://localhost:8080
https://tsrelay.example.ts.net/~moritz/amber-otter-lamp
hook h_3k2  ephemeral  channel default
connected
14:02:11 POST /payments/done → 200  12ms  tag:prod-worker  https://tsrelay.example.ts.net/inspect/c_7f3a
```

Configure that URL as the callback URL in the app that calls you. Paths below it are passed on: `…/amber-otter-lamp/payments/done` reaches your app as `/payments/done`.

If the CLI says you have no access, your operator needs to grant you the `receiver` role; the web UI shows them the exact grant.

## Hooks

A hook is a URL on the relay that delivers to whichever of your devices has a tunnel attached to it. `tsrelay http` creates one for you, or reuses the one it created before: running `tsrelay http 8080` again in the same directory prints the same URL, so you can keep it in your `.env`.

Useful flags:
- `-n`/`--name payments/v1` picks the path yourself.
- `-p`/`--persistent` keeps the hook after the command exits. Without it, the hook is *ephemeral*: the relay deletes it once no tunnel has been attached for a while. While no tunnel is attached to a persistent hook, callers get `561` with `Retry-After`.
- `--relay host` points at a relay that isn't at `tsrelay.<your tailnet>`.

`tsrelay http` also accepts `-- cmd args...` to start a local process with `TSRELAY_URL` set in its environment, and stop it (SIGTERM, then a kill after 5 s) when the tunnel ends:

```bash
tsrelay http 8080 -- npm run dev
```

The command keeps the `TSRELAY_URL` it was started with, even if the hook's path changes while it runs.

## Public hooks

A hook can also be made reachable from the internet, for third-party webhooks (Stripe, GitHub, Twilio…) that can't reach your tailnet. It's opt-in twice over: the operator runs the relay with `--funnel` and grants you `"public": true` (see [Serving public hooks](#serving-public-hooks)), and you mark the hook `public`:

```bash
tsrelay http 3000 --public      # make this hook's URL reachable from the internet
```

Without the `public` grant, turning a hook public fails. The relay prints both URLs; the public one is a secret, so treat it like one, and rotate it with `tsrelay hooks rotate-url <hook>` if it leaks. An ephemeral `tsrelay http --public` hook gets a new public URL once it's reaped (after the relay's `--ephemeral-grace`, 10 minutes by default, without a tunnel), so use `-p`/`--persistent` (or `tsrelay hooks create --public`) for a stable URL to give to a provider. The relay does not verify provider signatures — see [SECURITY.md](SECURITY.md).

## Channels

Every hook belongs to a channel, `default` unless you pick another with `--channel`. Your grant says which channels you may create hooks in, and senders' grants say which channels they may call. So a channel decides who can reach your hook. Ask your operator which channels exist on your tailnet.

The web UI's Channels page lists your patterns and the channels your hooks are in, and each channel's page shows its hooks and whether you may receive and send on it. Channel names in the hook list link there, and the hook list can be filtered by channel. The same data is in the API: `GET /channels`, `GET /channels/{name}`, and `GET /hooks?channel=prod.payments` for the hooks in one channel. Who else may send on a channel isn't something the relay knows; that's in the tailnet policy.

## Managing hooks

```bash
tsrelay hooks list                                 # your hooks (alias: ls)
tsrelay hooks show <hook>                          # one hook and its effective settings
tsrelay hooks create [<path>] [flags]              # create a persistent hook
tsrelay hooks update <hook> [flags]                # change one (only the given flags)
tsrelay hooks delete <hook>...                     # delete one or more (alias: rm)
tsrelay hooks rotate-url <hook>                    # give a public hook a new public URL
tsrelay url [<port|url>]                           # the URL tsrelay http would use, without contacting the relay
tsrelay attach <hook>... -t <port|url>             # serve existing hooks from one local target
```

`<hook>` is a hook's ID or path. `-k`/`--insecure` (on `http` and `attach`) skips TLS certificate verification for an `https://` target.

If a served hook's path is changed (`tsrelay hooks update`, the web UI or the API), `http` and `attach` print its new URL on stdout, and `tsrelay http` remembers it.

## Hook settings

Every hook has delivery settings. Your relay sets a default and, for most of them, a maximum; a hook may only choose values within the maximum.

| Setting | Flag | |
|---|---|---|
| `attachPolicy` | `--attach-policy` | `takeover` replaces an attached tunnel with a new one; `reject` refuses the new one with `409` |
| `hostHeader` | `--host-header` | `preserve` passes the relay's host to your app; `rewrite` sets `Host` to your local target's host |
| `holdTimeout` | `--hold-timeout` | How long a call waits for a tunnel to attach; `0s` disables holding |
| `responseTimeout` | `--response-timeout` | How long to wait for your app's response headers |
| `idleTimeout` | `--idle-timeout` | Longest gap in body traffic, either direction, before the call fails |
| `maxBodyBytes` | `--max-body-bytes` | Request body size limit; `0` is unlimited |
| `rateLimit` | `--rate-limit` | Calls per second and burst, as `perSecond/burst` |
| `capture` | `--capture` | Keep request and response bodies for the inspector, which relays don't do by default; metadata and headers are always recorded |

Set them when creating or updating a hook:

```bash
tsrelay hooks create payments/v1 --rate-limit 10/20 --host-header rewrite
tsrelay hooks update payments/v1 --hold-timeout 0s
```

`tsrelay http` takes the same flags; they apply when it creates the hook, not to a hook it reuses. `tsrelay hooks show <hook>` lists the effective values, defaults included.

A hook's `public` flag is also a setting (`--public`), but it has no relay-wide default or maximum: it's governed by the `public` grant and `--funnel` instead — see [Public hooks](#public-hooks). Unlike the other settings, `tsrelay http --public` also changes a hook it reuses.

## What your app receives

Bodies arrive exactly as sent, byte for byte, so signatures from Stripe, GitHub, Svix, or [Standard Webhooks](https://www.standardwebhooks.com/) verify normally. The relay adds these headers, after removing any copies the sender sent:

| Header | Value |
|---|---|
| `Tsrelay-Sender-Node` | The calling machine's name |
| `Tsrelay-Sender-Tags` | Its tags, comma-separated |
| `Tsrelay-Sender-Login` | Its user, if it is not a tagged machine |
| `Tsrelay-Sender-Public` | `true` for a call from the internet to a [public hook](#public-hooks); such a call has no `Tsrelay-Sender-Node`, `-Tags` or `-Login` |
| `Tsrelay-Hook` | `user/path` of the hook |
| `Tsrelay-Call-Id` | Also returned to the caller, for matching logs |
| `X-Forwarded-For`, `-Host`, `-Proto`, `-Prefix` | The original request, for URL-signing schemes |

## Web UI

Open the relay's URL (`https://tsrelay.<your tailnet>/`) in a browser on your tailnet. You see your hooks with their tunnels and latest calls, and can create, edit and delete hooks. Each hook's page shows its calls as they arrive, and lets you send it a test request. A call's page shows the request and the response side by side, and lets you replay the call or copy it as a curl command (aimed at your local target or at the hook URL). Each call line `tsrelay http` prints ends with a link to that call.

Each hook's page has the call list on the left and the selected call on the right; the list updates live, and you can filter it by status, method, path and sender. Keyboard shortcuts: `j`/`k` move through the calls, `Enter`/`Esc` select and deselect, `/` focuses the filter, `r` replays, `c` copies as curl, `t` sends a test request, and `?` lists them. The UI needs JavaScript; the CLI and the [API](#the-api) don't.

## Inspecting calls

The relay keeps the most recent calls of each hook in memory (100 unless your operator changed it): method, path, sender, status and timings, the query and the headers (minus redacted ones such as `Authorization`), and the start of each body if the hook captures bodies, which it doesn't unless you or your operator turn capture on. Only you see your hooks' queries, headers and bodies; admins see metadata only. Calls are lost when the relay restarts.

| | |
|---|---|
| `GET /hooks/{id}/calls` | Call summaries, newest first; with `Accept: text/event-stream`, each call as it arrives and completes |
| `GET /hooks/{id}/calls/{callID}` | One call with its captured headers and bodies |
| `POST /hooks/{id}/calls/{callID}/replay` | Send the call again; its body and query string must have been captured in full |
| `POST /hooks/{id}/test` | Send `{"method","path","headers","body"}` through the relay to your app |

Replays and test requests go through the whole relay to your app. The answer is recorded in the inspector and returned by the API, never sent to the original sender. Every response a sender gets carries `Tsrelay-Call-Id`, which names the call here.

## The API

The web UI's hook pages are also API resources at the same URLs: ask for JSON and you get the data the page shows. A `.json` or `.html` extension picks the representation; otherwise the `Accept` header does, and JSON is the default. So the URL in your browser's address bar is the one to script against:

```bash
curl https://tsrelay.<your tailnet>/hooks                               # your hooks, as JSON
curl https://tsrelay.<your tailnet>/hooks/h_3k2/calls.json              # the same as the hook page's call list
curl -H 'Accept: text/event-stream' https://tsrelay.<your tailnet>/hooks  # live changes to your hooks
```

`/` is the entry document (who you are, your limits, and links to the rest), and `/hooks/{id}` streams changes to one hook. The relay identifies you by your tailnet identity, so there are no tokens. The full description is an OpenAPI document at `/openapi.json` (or `/openapi.yaml`).

## Errors

Errors produced by the relay itself are `application/problem+json` responses and carry a `Tsrelay-Error` header. Responses from your app are never marked, including its own 5xx. Tunnel and transport failures have statuses of their own, 561 to 565, so they can't be mistaken for your app's 502, 503 or 504; clients that don't know them treat them as 500. Senders see these:

| `Tsrelay-Error` | Status | Meaning |
|---|---|---|
| `sender_forbidden` | 403 | The caller has no sender grant |
| `hook_not_found` | 404 | No such hook, not in the caller's channels, or it was deleted/expired while a call was held for it |
| `body_stalled` | 408 | The sender's request body stalled past `idleTimeout` |
| `body_too_large` | 413 | Over the hook's `maxBodyBytes`, or for a call from the internet, over the relay's `--funnel-max-body-bytes` |
| `rate_limited` | 429 | Over the hook's `rateLimit`; `Retry-After` is the limiter's wait, rounded up to whole seconds |
| `tunnel_unavailable` | 561 | No tunnel attached within `holdTimeout`, or too many calls are already waiting; `Retry-After` is `holdTimeout`, but never less than 5 s |
| `tunnel_overloaded` | 562 | Too many concurrent calls on the tunnel; `Retry-After: 1` |
| `tunnel_lost` | 563 | The tunnel dropped mid-call |
| `upstream_unreachable` | 564 | The tunnel is up but your app is not listening |
| `upstream_timeout` | 565 | Your app did not answer within the hook's `responseTimeout` |

## Completion and output

`tsrelay completion bash|zsh|fish|powershell` prints a completion script; for example `tsrelay completion fish > ~/.config/fish/completions/tsrelay.fish`. Besides commands and flags, it completes your hooks (by path and ID, for `attach` and `hooks show|update|delete`), channels for `--channel` (those your grants name, and those your hooks use), and the targets `tsrelay http` remembered in this directory for `--to` and `<port|url>`. These ask the relay live and give up after 2 seconds; when anything fails, they offer nothing and print nothing.

Every flag can also be set in the environment as `TSRELAY_<FLAG>`, with dashes as underscores (`--relay` ← `TSRELAY_RELAY`, `--hold-timeout` ← `TSRELAY_HOLD_TIMEOUT`); the command line wins.

On a terminal, output is colored, call lines are aligned, and each call ends with its ID as a clickable link to the inspector. Output that isn't a terminal is plain text, as is output with `NO_COLOR` set or `--no-color` given. If your terminal doesn't support hyperlinks, `--no-hyperlinks` (or `TSRELAY_NO_HYPERLINKS=1`) prints the full inspector URL instead.

`-j`/`--json` (on every command) prints JSON on stdout instead of text. `http` and `attach` print one event per line (`url`, `connected`, `disconnected`, `call`, `stopped`), and the other commands print one value. On a terminal the JSON is indented; piped, it's compact.

Errors go to stderr and say what went wrong, why, and what to try instead:

```
✗ No target to forward calls to
  attach needs to know where your local app listens.

  Try:
    tsrelay attach stripe --to 3000
    tsrelay attach stripe --to http://localhost:8080
```

With `--json` they are `{"error": {"title": …, "reason": …, "fixes": […], "code": …}}`, where `code` is the relay's `Tsrelay-Error` code for a refusal, `usage` for a mistake on the command line, or one of the CLI's own (such as `relay_unreachable`). A usage error exits with status 2, any other with 1.

## Open-source licenses

tsrelay is built from open-source projects whose licenses ask for their notices to travel with it. The web UI's footer links to the licenses page (`/licenses`), which lists each one with its license and notice texts, whatever roles you have. `tsrelay licenses` prints the same notices, and the relay's licenses page if it knows your relay. The relay also serves them as one text file at `/notices.txt`, and the software bill of materials they come from at `/sbom.cdx.json` (CycloneDX).

# Running a relay

This half is for operators: whoever adds the relay to the tailnet and decides who may use it.

## Tailnet policy and roles

The relay is a tailnet node tagged `tag:tsrelay`. Access to it comes from [grants](https://tailscale.com/kb/1324/grants): the `ip` part lets a device reach the relay at all, and the `app` part, the `mazetti.me/cap/relay` [app capability](https://tailscale.com/docs/features/access-control/grants/grants-app-capabilities), says what it may do there. A device without a value for the capability has no access; the web UI tells such users exactly that, and shows a grant that would fix it.

A minimal policy:

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

Each capability value is an object with these fields:

| Field | |
|---|---|
| `roles` | Any of `receiver`, `sender`, `admin` (see below) |
| `channels` | Channel patterns the roles apply to; `*` matches anything, dots included. Default `["*"]` |
| `maxHooks` | Receivers: hooks owned at once (relay default 10) |
| `maxTunnels` | Receivers: tunnels attached at once (relay default 5) |
| `maxHookTTL` | Receivers: the longest a persistent hook may live, as a Go duration like `720h` (relay default: unlimited) |
| `public` | Receivers: `true` lets them make their hooks reachable from the internet; see [Serving public hooks](#serving-public-hooks) |

| Role | May |
|---|---|
| `receiver` | Create and manage their own hooks in channels their patterns match, attach tunnels, inspect and replay their own calls |
| `sender` | Call any hook whose channel their patterns match. Tagged machines can only ever be senders |
| `admin` | List, view the metadata of, and delete everyone's hooks and tunnels. Admins **cannot** see other users' captured queries, headers or bodies, and cannot replay their calls |

Channels scope senders to receivers:

```jsonc
{ "roles": ["receiver"], "channels": ["staging", "prod.*"] }   // may create hooks in these channels
{ "roles": ["sender"],   "channels": ["staging"] }             // may only call staging hooks
```

Scope sender grants first, because they are the broad ones.

When several grants match a device, the relay combines them: roles and channels are unioned (channels per role, so a sender grant never widens what the same device may do as a receiver), and each limit is the highest any grant sets, falling back to the relay default. A grant can therefore only raise a limit; to restrict most users, lower the relay default and raise it for specific groups. Revoking a grant takes effect within one `--revalidate-interval` (60 s), also for attached tunnels. The details are in [DESIGN.md §5](docs/DESIGN.md#5-authorization).

## Making someone an admin

Admin is a role like the others: grant it to a group in its own grant, or add it to an existing one. For example, to let `group:platform` receive callbacks and administer the relay:

```jsonc
"groups": { "group:platform": ["alice@example.com", "bob@example.com"] },
"grants": [
  { "src": ["group:platform"], "dst": ["tag:tsrelay"], "ip": ["tcp:443"],
    "app": { "mazetti.me/cap/relay": [{ "roles": ["receiver", "admin"] }] } }
]
```

Admins get an **Admin** page in the web UI listing every hook and tunnel, as metadata only. An admin without the `receiver` role starts on the admin page and can't create hooks. The role takes effect on the admin's next request; no relay restart is needed.

## Run the relay

Enable [HTTPS certificates](https://tailscale.com/kb/1153/enabling-https) for your tailnet, or run the relay with `--tls=false` and grant `tcp:80` instead of `tcp:443`.

```bash
TS_CLIENT_SECRET=tskey-client-… tsrelay serve
```

Use an OAuth client secret with the `tag:tsrelay` tag. Auth keys (`TS_AUTHKEY`) also work, but they expire. State lives in `--state-dir`: `/var/lib/tsrelay` in containers, `~/.local/state/tsrelay` otherwise. Run exactly one relay per state directory.

The relay serves the web UI, the API and the CLI downloads on its own URL, so users need nothing else: point them at [Using tsrelay](#using-tsrelay).

## Serving public hooks

To let users make hooks reachable from the internet (see [Public hooks](#public-hooks)), run the relay with `--funnel`. The tailnet policy also needs the `funnel` node attribute for the relay, and `"public": true` on the grants of the users who may make hooks public — see [deploy/policy.example.hujson](deploy/policy.example.hujson) for both.

```bash
tsrelay serve --funnel          # serve public hooks through Tailscale Funnel
```

## Configuration

Every flag can also be set as an environment variable: `--ephemeral-grace` becomes `TSRELAY_EPHEMERAL_GRACE`. Run `tsrelay serve --help` for the full list. The most relevant flags:

| Flag | Default | |
|---|---|---|
| `--hostname` | `tsrelay` | Tailnet machine name |
| `--tls` | `true` | HTTPS on 443; `false` serves HTTP on 80 |
| `--funnel` | `false` | Serve hooks their owners made public on the internet, through Tailscale Funnel; requires `--tls` |
| `--funnel-port` | `443` | The Funnel port for public hooks: `443`, `8443` or `10000` |
| `--funnel-max-body-bytes` | `26214400` (25 MiB) | Largest request body of a call from the internet |
| `--advertise-tags` | `tag:tsrelay` | |
| `--cap-name` | `mazetti.me/cap/relay` | Capability that carries grants |
| `--user-slug` | `local-part` | `moritz@example.com` → `~moritz`; `login` uses the full login |
| `--user-slug-override` | | `alice@b.com=alice-b`, for collisions |
| `--max-hooks-default` / `--max-tunnels-default` | `10` / `5` | Per user; grants can raise them with `maxHooks` / `maxTunnels` |
| `--max-hook-ttl-default` | unlimited | Longest a persistent hook may live; grants can raise it with `maxHookTTL` |
| `--ephemeral-grace` | `10m` | How long an ephemeral hook survives without its tunnel |
| `--tunnel-concurrency` | `32` | Concurrent calls per tunnel |
| `--hold-queue` | `100` | Calls per hook that may wait for a tunnel to attach at once |
| `--revalidate-interval` | `60s` | How often an attached tunnel's grant is re-checked |
| `--capture-calls` | `100` | Calls per hook the inspector keeps in memory |
| `--capture-limit` | `65536` | Bytes the inspector keeps of each body, of each call's headers, and of its query string |
| `--capture-bodies` | `true` | `false` keeps no bodies for any hook |
| `--redact-headers` | `Authorization,Proxy-Authorization,Cookie,Set-Cookie,X-Api-Key` | Headers whose values are never kept; an empty value keeps these defaults, so redaction cannot be turned off |
| `--ops-listen` | off | Host address (e.g. `:9090`) for `/healthz`, `/readyz` and `/metrics`; never on the tailnet |
| `--ops-pprof` | `false` | Serve Go's profiler under `/debug/pprof/` on the ops listener |
| `--otel` | `false` | Export traces and metrics over OTLP/HTTP; see [Tracing](#tracing) |
| `--trace-propagation` | `true` | With `--otel`: continue a sender's `traceparent` and pass the relay's own to the app, so one trace spans sender, relay and app; `false` forwards trace headers untouched |

The audit log is JSON on stderr. It never contains bodies, query strings, or header values.

## Hook defaults and limits

Each [hook setting](#hook-settings) has a relay-wide default, `--hook-<setting>-default`, and the numeric and duration settings also a maximum, `--hook-<setting>-max`:

| Setting | Default | Maximum |
|---|---|---|
| `attachPolicy` | `takeover` | |
| `hostHeader` | `preserve` | |
| `holdTimeout` | `30s` | `5m` |
| `responseTimeout` | `2m` | `30m` |
| `idleTimeout` | `60s` | `30m` |
| `maxBodyBytes` | unlimited | none |
| `rateLimit` | `100/200` | none |
| `capture` | `false` | |

For example `--hook-rate-limit-default=100/200`, or `--hook-max-body-bytes-default=1048576 --hook-max-body-bytes-max=10485760` (the default body size is unlimited, which a maximum would not allow, so set both). A hook may only set a value within the maximum.

## Deployment

Ship it as:
- a static binary (`go build ./cmd/tsrelay`, or download one from a [release](../../releases))
- a container image, `ghcr.io/radiergummi/tsrelay` (see the `Dockerfile`)
- Kubernetes, with `deploy/kubernetes/` (a `StatefulSet` with `replicas: 1`, a `Secret` example, no `Service`: the relay is reached over the tailnet only)
- Docker Compose, with `deploy/compose.yaml`

Run exactly one relay per state directory (`--state-dir`); see DESIGN.md §11 and §14.

## Metrics

With `--ops-listen=:9090`, the relay serves `/healthz` (process alive), `/readyz` (tailnet joined and the hook store loaded) and `/metrics` (Prometheus text format: the counters and gauges `tsrelay_calls_total`, `tsrelay_call_bytes_total`, `tsrelay_tunnels`, `tsrelay_hooks`, `tsrelay_held_calls`, `tsrelay_authz_denials_total`; the latency histograms `tsrelay_call_duration_seconds`, `tsrelay_call_ttfb_seconds`, `tsrelay_call_hold_seconds`, `tsrelay_api_request_duration_seconds`; and Go runtime metrics). The ops listener is never on the tailnet interface.

## Tracing

With `--otel`, the relay exports traces and metrics over OTLP/HTTP. Everything else is configured with the standard OpenTelemetry variables: `OTEL_EXPORTER_OTLP_ENDPOINT` (default `http://localhost:4318`), `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_TRACES_SAMPLER`, `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES`. An unreachable collector never stops the relay; it logs a warning at most once a minute. Spans and metrics never contain bodies, query strings, or header values.

Each call is a `tsrelay.call` span with children `tsrelay.authorize`, `tsrelay.hold` (only when the call waited for a tunnel) and `tsrelay.forward` (until the app's response headers). With `--trace-propagation` (the default), the relay joins the sender's trace and replaces `traceparent` on the way to the app, so the app's own spans nest under `tsrelay.forward`. Without `--otel`, trace headers pass through untouched.

## Profiling

With `--ops-pprof`, the ops listener also serves Go's profiler under `/debug/pprof/`. On Kubernetes, enable it temporarily and reach it through a port-forward:

    kubectl port-forward statefulset/tsrelay 9090:9090
    go tool pprof http://localhost:9090/debug/pprof/heap

## Verifying a download

Each release binary comes with a SHA-256 checksum, a signed build provenance attestation, and a signed CycloneDX SBOM listing the modules it was built from. The macOS binaries are also signed with a Developer ID and notarized. With the GitHub CLI:

```bash
gh attestation verify tsrelay_linux_amd64 -R Radiergummi/tsrelay                              # provenance
gh attestation verify tsrelay_linux_amd64 -R Radiergummi/tsrelay --predicate-type https://cyclonedx.org/bom
```

Without access to GitHub's API, verify against the bundles attached to the release: add `--bundle tsrelay.provenance.sigstore.json` or `--bundle tsrelay_linux_amd64.sbom.sigstore.json`. The SBOM itself is `tsrelay_linux_amd64.cdx.json`.

# Contributing

Building, testing and releasing tsrelay is described in [CONTRIBUTING.md](CONTRIBUTING.md), and its design in [docs/DESIGN.md](docs/DESIGN.md).

# License

Apache License 2.0; see [LICENSE](LICENSE) and [NOTICE](NOTICE).
