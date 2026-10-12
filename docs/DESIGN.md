# tsrelay design

Status: draft for review · 2026-10-12

tsrelay is a callback relay for tailnets. It lets services in one part of a tailnet (typically production) deliver HTTP callbacks to applications running on developer machines, without ever opening a connection to those machines and without anything being publicly reachable.

## 1. Problem and goals

Applications use callback URLs for async and long-running work. An engineer often runs one application locally while the application it talks to runs in production. The production application then has to call a URL on the engineer's laptop. Tailscale ACLs rightly forbid production nodes from connecting to developer machines.

tsrelay is a reverse relay. The relay is its own tailnet node. Developer machines dial out to it and attach tunnels. Production services send callbacks to the relay, which forwards each one down the matching tunnel and returns the local application's response.

**Goals**

- Production never connects to a developer machine. Developer machines only make outbound connections to the relay.
- Authorization comes only from the tailnet policy, through Tailscale application capabilities read with `WhoIs`. There is no separate identity system.
- Callbacks are forwarded faithfully: streaming, any body size, byte-exact bodies, and the real response status.
- A zero-config installation works, and every value that is specific to an organisation can be configured.
- It runs as a standalone binary, a container under Docker, Compose or Swarm, and a Kubernetes workload.
- A web UI for managing hooks and inspecting calls, in the style of ngrok's inspector.

**Non-goals**

- Public exposure. The relay never enables Funnel and never listens outside the tailnet, apart from an optional ops listener (§13).
- Intercepting or hijacking services, as Telepresence or mirrord do. tsrelay delivers only to URLs that were explicitly created as hooks.
- Transforming traffic: no header rewrite rules, no payload transformation, and no signature verification at the relay.
- Multiple replicas in v1 (see §17).

## 2. Concepts

| Term | Meaning |
|---|---|
| **Relay** | The `tsrelay serve` process. It is one tailnet node (via `tsnet`), by default named `tsrelay` and tagged `tag:tsrelay`. |
| **Hook** | A callback endpoint owned by a tailnet user. It has a URL path, a channel and behaviour settings. A hook exists whether or not any tunnel is attached. |
| **Tunnel** | A connection from a user's device to the relay. It serves one or more of that user's hooks and forwards their calls to local targets. |
| **Channel** | A concrete label on a hook, such as `default`, `prod` or `prod.eu`. Grants use channel patterns to link senders to receivers. |
| **Call** | One request received for a hook, from a sender, a replay or a test. |
| **Attempt** | One try at delivering a call down a tunnel. A call has one or more attempts. |

A hook is **ephemeral** or **persistent**.

- **Ephemeral hooks** are deleted once no tunnel has been attached for a grace period (10 min by default). This is the default for `tsrelay http`.
- **Persistent hooks** stay until they are deleted or reach an optional expiry. If a call arrives while no tunnel is attached, the relay holds it for the reconnect window (§7.3) and then answers `503` with `Retry-After`.

## 3. Architecture

```
 prod service (tag:prod-worker)                     developer laptop
        │  HTTPS over tailnet                          ┌──────────────────────┐
        ▼                                              │ tsrelay http 8080     │
 ┌──────────────────────────────────────────┐ tunnel   │  (HTTP/2 server)      │
 │ tsrelay serve  (tsnet node "tsrelay")     │◀─────────│  dials out, attaches  │
 │                                           │ (HTTP/2, │                       │
 │  /~{user}/{hook}/…  callback endpoint     │ reversed)│  forwards to          │
 │  /api/v1/…          JSON API + tunnel     │          │  http://localhost:8080│
 │  /                  web UI                │          └──────────────────────┘
 │                                           │
 │  authz (WhoIs + CapMap) · hook store ·    │
 │  tunnel registry · inspector · audit log  │
 └──────────────────────────────────────────┘
```

There is one binary, `tsrelay`, and it has two roles:

- **`tsrelay serve`** runs the relay. It embeds `tsnet`.
- **Every other subcommand** is a client: `http`, `attach`, `hooks`, `url` (§15). Clients do **not** embed `tsnet`. They reach the relay through the machine's own Tailscale connection, so the relay's `WhoIs` sees the developer's real user and node.

### 3.1 Package layout (proposed)

```
cmd/tsrelay/            main, subcommand wiring
internal/authz/         capability parsing, merging, channel matching, Identity
internal/identify/      Identifier interface; tsnet WhoIs implementation; test fake
internal/hooks/         Hook model, validation, name generation, Store interface + JSON store
internal/tunnel/        tunnel protocol: relay side (registry, round-tripper) and client side
internal/relay/         callback handler, forwarding, hold-for-reconnect, limits
internal/inspect/       call/attempt capture, ring buffers, replay, event fan-out
internal/api/           JSON API handlers, problem responses, CSRF/host checks
internal/ui/            server-rendered pages, embedded static assets
internal/client/        CLI-side API client, local state, forwarding target
internal/audit/         audit event types and slog emission
internal/metrics/       hand-written Prometheus text exposition
```

The relay core (`relay`, `api`, `tunnel`) receives a `net.Listener` and an `Identifier`. It never touches `tsnet` directly. This boundary is what makes the design testable without a tailnet (§16).

## 4. URL model

```
https://tsrelay.<tailnet>.ts.net/~{user}/{hook-path}/{remainder...}
```

- **`{user}`** is the owner's *slug*. It is derived from the Tailscale login (§5.6).
- **`{hook-path}`** is the hook's path. It has 1–8 segments, each matching `[A-Za-z0-9._-]+`, and is at most 128 characters in total. By default it is three random words such as `amber-otter-lamp`, drawn from an embedded wordlist (the EFF short list, attributed in `NOTICE`). Users may choose any valid path.
- **`{remainder}`** is forwarded to the local target. The prefix `/~{user}/{hook-path}` is stripped, and the original prefix is sent as `X-Forwarded-Prefix`.
- **Routing** uses the longest matching hook path within the user's namespace. **Nested hooks are rejected:** a user cannot own both `foo` and `foo/bar`, so routing is never ambiguous.
- **Reserved paths:** every path that does not start with `/~` belongs to the relay (the UI at `/`, the API at `/api/v1/`, `/static/`). Hooks therefore never collide with relay endpoints.
- **The query string** is forwarded unchanged. It is never written to the audit log (§13).

## 5. Authorization

### 5.1 Identity

Every incoming connection is resolved with `LocalClient.WhoIs(remoteAddr)`. In `tailscale.com@v1.104.0` it returns `apitype.WhoIsResponse{Node, UserProfile, CapMap}`, where `CapMap` is `tailcfg.PeerCapMap` = `map[peercap.Cap][]tailcfg.RawMessage`. The result is converted into an `authz.Identity`:

```go
type Identity struct {
    UserID    tailcfg.UserID // zero for tagged nodes
    Login     string         // e.g. "moritz@example.com"; empty for tagged nodes
    NodeID    tailcfg.StableNodeID
    NodeName  string
    Tags      []string       // non-empty means a tagged node
    Grants    Effective      // merged capability (§5.3)
}
```

For a tagged node, `UserProfile` is Tailscale's generic "tagged-devices" placeholder. Tagged nodes can therefore **only act as senders**. Hooks are always owned by users. Ownership is keyed by the stable `UserID`, and `Login` is used only for display.

### 5.2 Capability schema

The capability name is configurable with `--cap-name`. The default is **`mazetti.me/cap/relay`**. Each grant contributes one JSON object:

```jsonc
{
  "roles":      ["receiver"],          // any of: "receiver", "sender", "admin"
  "channels":   ["prod", "staging.*"], // channel patterns; default ["*"]
  "maxHooks":   10,                    // receiver: hooks owned at once
  "maxTunnels": 5,                     // receiver: tunnels attached at once
  "maxHookTTL": "720h"                 // receiver: cap on a hook's expiry; Go duration
}
```

| Role | May |
|---|---|
| `receiver` | Create and manage their own hooks in channels matched by their patterns, attach tunnels, inspect and replay their own calls. |
| `sender` | Call any hook whose channel is matched by their patterns. |
| `admin` | List, view metadata of, and delete everyone's hooks and tunnels. Admins **cannot** see other users' captured headers or bodies, and cannot replay their calls. |

A node with no value for the capability has no access. The UI tells such a user exactly that, and shows a policy snippet that would grant access.

A minimal policy that matches the zero-config defaults:

```jsonc
"tagOwners": { "tag:tsrelay": ["autogroup:admin"] },
"grants": [
  { "src": ["autogroup:member"], "dst": ["tag:tsrelay"], "ip": ["tcp:443"],
    "app": { "mazetti.me/cap/relay": [{ "roles": ["receiver"] }] } },
  { "src": ["tag:prod-worker"],  "dst": ["tag:tsrelay"], "ip": ["tcp:443"],
    "app": { "mazetti.me/cap/relay": [{ "roles": ["sender"] }] } }
]
```

The grant's `ip` part is what gives network access. The `app` part only authorizes inside the relay.

### 5.3 Merging multiple grants

`CapMap[cap]` is a flat list of JSON values from every grant that matches, and it does not say which grant each value came from. The relay merges the list into an `Effective` value:

- **Each value is parsed on its own.** A malformed value is ignored and logged once per node (fail closed); the others still apply. **Unknown fields are ignored**, which keeps the schema forward-compatible.
- **`roles` and `channels`** are combined by union. Channels are tracked per role, so a `sender` grant's channels never widen what the same node can do as a `receiver`.
- **Numeric and duration limits:** for each value, take the value it sets, or the relay's configured default if it sets none. The effective limit is the **maximum** of those. Grants only ever add access in Tailscale, and merging does the same. As a result a grant cannot push a limit *below* the relay default. To restrict most users, an operator lowers the relay default and raises it for specific groups.

### 5.4 Channels and patterns

- **Channel names** are dot-separated segments of `[a-z0-9-]`, for example `default`, `prod`, `prod.eu`, `staging.payments`. A hook has exactly one channel, which defaults to `default`.
- **Patterns** appear only in grants. `*` matches any run of characters, dots included. So `*` matches everything, and `prod.*` matches `prod.eu` and `prod.eu.west` but not `prod`. Matching is always a pattern against a concrete name, never one pattern against another.
- **A receiver** may create a hook in channel `c` if one of their receiver patterns matches `c`.
- **A sender** may call a hook in channel `c` if one of their sender patterns matches `c`.
- **Defaults:** a value without `channels` means `["*"]`. The documentation points out that `*` on a *sender* grant is the broad one, and recommends scoping senders first.

### 5.5 Checks and revocation

| Moment | Check |
|---|---|
| Every callback request | `WhoIs` on the connection: the caller must be a sender whose patterns match the hook's channel. |
| Every API request | `WhoIs`. Writes are re-checked against the effective grant: channel, limits, TTL. |
| Tunnel attach | `WhoIs`: the caller must own every hook it attaches, be a receiver, and stay within `maxTunnels`. |
| Attached tunnels, every 60 s (configurable) | `WhoIs` again. A tunnel that is no longer authorized is detached with reason `revoked`. Hooks whose channel the owner no longer matches stop delivering (`403` on the API, `404` to senders) until the grant comes back. They are not deleted. |

Because a hook can only deliver through a live tunnel from its owner, revoking a grant takes effect within one revalidation interval, even for persistent hooks.

### 5.6 User slugs

- **Default** (`--user-slug=local-part`): the slug is the local part of the login, lowercased, with characters outside `[a-z0-9._-]` replaced by `-`. So `moritz@example.com` becomes `moritz`.
- **Alternative** (`--user-slug=login`): the full login, sanitized the same way.
- **Overrides** (`--user-slug-override=alice@b.com=alice-b`) resolve collisions explicitly.
- **Bindings are persisted:** the first user to create a hook under a slug is recorded in the store. A different user who maps to a taken slug cannot create hooks, and gets an error explaining the override option. This stops someone with a colliding login from taking over another user's URLs.

### 5.7 Browser-facing protections

Authority in the UI and API comes from the network identity, much like a cookie. The relay therefore defends against cross-site attacks and DNS rebinding:

- **Host check:** every request's `Host` must be one of the relay's MagicDNS names or tailnet IPs. Anything else gets `421`.
- **API writes** require `Content-Type: application/json`. If the `Sec-Fetch-Site` header is present it must be `same-origin` or `none`.
- **No CORS headers**, ever.
- **The UI sends a strict CSP:** `default-src 'self'` with no inline script. Captured headers and bodies are always rendered as text, never as HTML.

## 6. Tunnel protocol

### 6.1 Handshake

1. The client sends `GET /api/v1/tunnel?hook={id}&hook={id}…` with `Connection: Upgrade` and `Upgrade: tsrelay-tunnel/1`, using HTTP/1.1. Over TLS, the client offers only the `http/1.1` ALPN value so the connection can be taken over.
2. The relay authorizes the request (§5.5). On failure it answers with an ordinary problem response (§7.4).
3. On success the relay answers `101 Switching Protocols` and takes over the connection.
4. **The roles reverse:** the relay becomes the HTTP/2 *client* (`http2.Transport.NewClientConn`), and the CLI becomes the HTTP/2 *server* (`http2.Server.ServeConn`).

The protocol version lives in the `Upgrade` token. An incompatible change gets a new token, and the relay can accept several during a transition.

### 6.2 Streams

- **Forwarded calls:** each call attempt is one HTTP/2 request from the relay to the CLI. It carries a `Tsrelay-Hook: {id}` header, which the CLI uses to choose that hook's local target. The CLI removes all `Tsrelay-*` tunnel headers before forwarding, except the identity headers (§7.2).
- **Control messages:** requests with the header `Tsrelay-Control: 1` and a JSON body. They are handled by the CLI itself and never forwarded. Types: `attached` (the hook URLs, sent first), `replaced`, `revoked`, `hook-deleted`, `hook-updated`. Sender requests cannot forge them, because the relay strips every incoming `Tsrelay-*` header.
- **Status:** the CLI reports its local target per hook when it attaches, as a query parameter on the handshake. The UI uses this for display and for copy-as-curl.

### 6.3 Liveness and reconnect

- **Keepalive:** both sides send HTTP/2 PINGs every 15 s. A connection is declared dead after 2 missed PINGs.
- **Reconnect:** the CLI reconnects with exponential backoff and full jitter (from 250 ms up to 30 s) and attaches the same hooks again.
- **Takeover:** when a second tunnel attaches to a hook that already has one, the hook's `attachPolicy` decides.
  - **`takeover`** (default): the new tunnel wins. The old one is sent `replaced` and detached from that hook. It is closed once it serves no hooks.
  - **`reject`:** the new attach fails with `409`.
- **Sessions:** each client process picks a random session ID and sends it with every attach. Redials after a lost connection are marked as resumes, and a resume may only displace a tunnel of its *own* session. A CLI that was taken over, but never received `replaced` (it was asleep, or its tunnel was saturated), therefore gets `409 conflict` when it redials, rather than taking the hook back. A stale, half-open tunnel from the same process is still replaced, which is the common case on a flaky connection.

### 6.4 Concurrency

The relay keeps its own per-tunnel semaphore, 32 by default. Acquiring it never blocks, so a call that would exceed the limit gets `503 tunnel_overloaded` immediately instead of queuing inside the HTTP/2 transport. The CLI advertises a matching `SETTINGS_MAX_CONCURRENT_STREAMS`.

## 7. Request handling

### 7.1 Pipeline

```
accept → host check → WhoIs → route (~user/hook) → sender authz → rate limit
       → attach call record → wait for tunnel (hold) → acquire stream slot
       → forward (streaming, both directions) → record attempt → done
```

Delivery is **synchronous pass-through.** The sender receives the local application's real status, headers and body, streamed as it arrives.

### 7.2 Forwarding rules

- **Bodies are byte-exact** in both directions. There is no parsing, re-encoding or decompression, so signatures over the raw body (Stripe, GitHub, Svix and Standard Webhooks) still verify. A test asserts this (§16).
- **Hop-by-hop headers** are removed as RFC 9110 requires. All other headers pass through, and so do trailers.
- **`Host`** is preserved by default (`hostHeader: preserve`). Setting `rewrite` sets it to the local target's host instead, for dev servers that reject unknown hosts.
- **Added headers:** `X-Forwarded-For` (the sender's tailnet IP), `X-Forwarded-Host`, `X-Forwarded-Proto` and `X-Forwarded-Prefix`. Together these let URL-signing schemes, such as Twilio's, rebuild the original URL.
- **Sender identity** is passed on to the local application. Every incoming `Tsrelay-*` header is deleted first, the same strip-then-set pattern `tailscale serve` uses (`ipn/ipnlocal/serve.go`). Then the relay sets:
  - `Tsrelay-Sender-Node`: the sender's node name
  - `Tsrelay-Sender-Tags`: its tags, comma-separated
  - `Tsrelay-Sender-Login`: set only if the sender is a user node
  - `Tsrelay-Hook`: the hook's `user/path`
  - `Tsrelay-Call-Id`: the call's ID
- **Response:** the relay adds `Tsrelay-Call-Id` to every response, so the sender's logs can be matched to the inspector.

### 7.3 Hold for reconnect

If a call arrives while no tunnel is attached to its hook, the relay waits up to `holdTimeout` (30 s by default) for one to attach before it forwards anything. Nothing has been read from the request body at that point, so streamed bodies are safe to hold. Held calls count against a per-hook limit of 100. Beyond that, calls fail immediately with `503`.

A call already **in flight** when its tunnel drops cannot be retried, because its body has been partly consumed. It fails with `502 tunnel_lost`. Buffered replay is on the backlog (§17).

### 7.4 Relay-originated errors

Errors produced by the relay itself use `application/problem+json` (RFC 9457) and carry a `Tsrelay-Error` header. That lets senders and developers tell them apart from responses produced by the local application, which pass through untouched, including the application's own 503s.

| `Tsrelay-Error` | Status | When |
|---|---|---|
| `sender_forbidden` | 403 | Caller has no `sender` role at all |
| `hook_not_found` | 404 | No such hook, or the sender's channels don't match it. The two cases are deliberately indistinguishable to the sender; the owner sees channel-denied calls in the inspector. |
| `body_too_large` | 413 | Over `maxBodyBytes` (unlimited by default) |
| `rate_limited` | 429 + `Retry-After` | Hook rate limit exceeded |
| `tunnel_unavailable` | 503 + `Retry-After` | No tunnel attached within `holdTimeout`, or the hold queue is full |
| `tunnel_overloaded` | 503 + `Retry-After: 1` | Per-tunnel concurrency limit reached |
| `upstream_unreachable` | 502 | The CLI could not connect to the local target. The CLI reports this as a 502 with its own `Tsrelay-Error`, which the relay passes through. |
| `tunnel_lost` | 502 | Tunnel dropped mid-call |
| `upstream_timeout` | 504 | No response headers within `responseTimeout`, or the body was idle for `idleTimeout` |

## 8. Hook settings

Every setting has a relay-wide default and a relay-wide maximum. Settings that a grant governs are also capped by the owner's effective grant.

| Setting | Default | Notes |
|---|---|---|
| `path` | three random words | §4 |
| `channel` | `default` | Must match one of the owner's receiver patterns |
| `description` | empty | Free text, at most 280 characters |
| `ephemeral` | `true` from `tsrelay http`, `false` from the UI or `hooks create` | Deleted after `ephemeralGrace` with no tunnel attached |
| `expiresAt` | none | Capped by `maxHookTTL` |
| `attachPolicy` | `takeover` | Or `reject` |
| `hostHeader` | `preserve` | Or `rewrite` |
| `holdTimeout` | 30 s | 0 disables holding |
| `responseTimeout` | 2 min | Time until response headers arrive |
| `idleTimeout` | 60 s | Longest gap in body traffic, in either direction |
| `maxBodyBytes` | unlimited | Request body |
| `rateLimit` | 100/s, burst 200 | Token bucket per hook |
| `capture` | `true` | Capture bodies for the inspector. Metadata is always recorded. |

## 9. Inspector

### 9.1 Data model

```
Call     { id, hookID, receivedAt, kind: sender|replay|test, sender: {node, tags, login, ip},
           method, path, query, requestHeaders (redacted), requestBody: Capture,
           outcome, attempts: []Attempt }
Attempt  { id, startedAt, tunnelID, node, waitedFor (hold), ttfb, duration,
           status, responseHeaders (redacted), responseBody: Capture,
           bytesIn, bytesOut, error: Tsrelay-Error code or "" }
Capture  { bytes (≤ captureLimit), size, truncated }
```

Every call has an attempts list from the start, even though v1 only ever creates one attempt per call. Replays create a new call of kind `replay` that refers to the original. Buffered retries (backlog) will add further attempts to the same call.

### 9.2 Storage and visibility

- **Retention:** the last 100 calls per hook (configurable), in memory, gone on restart. Each captured body is held up to 64 KiB (configurable). Calls that were denied by channel are recorded on the hook they targeted, so the owner can see them.
- **Visibility:** only the hook's owner sees query strings, headers and bodies. Admins see metadata only.
- **Redaction:** `Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie` and `X-Api-Key` are redacted by default. The list is configurable relay-wide. Redacted values are dropped at capture time and never held in memory.
- **Global switch:** operators can turn body capture off for the whole relay with `--capture-bodies=false`.

### 9.3 Actions

- **Replay:** re-sends a captured call down the hook's current tunnel, for calls whose request body was captured in full. Redacted headers are left out. The response goes only to the inspector, never back to the original sender. Only the owner can replay.
- **Copy as curl:** two variants. One targets the hook URL, which only works for nodes with the sender role. The other targets the tunnel's local target (e.g. `http://localhost:8080/…`), which is what developers usually want.
- **Send test request:** the owner writes a request (method, path, headers, body; prefilled with a small JSON sample) and sends it through the normal pipeline as a call of kind `test`. The sender authorization step is skipped, because the owner is calling their own hook. This proves the whole chain works on first setup.

## 10. Web UI and API

### 10.1 JSON API (`/api/v1`)

| Method & path | Purpose |
|---|---|
| `GET /me` | Identity, effective grant (roles, channel patterns, limits), current usage, user slug |
| `GET /hooks` | The caller's hooks (admins: `?all=true`) |
| `POST /hooks` | Create. `?dryRun=true` validates without saving. |
| `GET/PATCH/DELETE /hooks/{id}` | Read, update or delete. `PATCH` also accepts `?dryRun=true`. |
| `GET /hooks/{id}/calls` | Call summaries, newest first |
| `GET /hooks/{id}/calls/{callID}` | Full call with attempts and captures |
| `POST /hooks/{id}/calls/{callID}/replay` | Replay |
| `POST /hooks/{id}/test` | Send a test request |
| `GET /events` | Server-sent events: the caller's call, tunnel and hook changes |
| `GET /tunnel` | Tunnel upgrade (§6) |

- **Hook IDs** are short, opaque and random. Hook paths contain slashes, so they can't serve as IDs.
- **Validation errors** are `422` problem responses with a per-field `errors` list. A field error that comes from a grant names the limiting rule, for example "channel `billing` is not matched by your receiver patterns `prod`, `staging.*`".

The CLI uses this same API.

### 10.2 UI

The UI is server-rendered with `html/template`, plus a small vanilla JS file for live updates (over SSE) and dry-run validation as the user types. All assets are embedded with `embed.FS`, and there is no frontend build step. Pages:

- **Hooks:** your hooks, with URL, channel, tunnel status (attached node and local target), and the last call.
- **Create / edit hook:** the settings in §8. The channel picker is fed by `/me`, and fields are validated live with dry-run. Limits from the grant are shown next to each field.
- **Hook detail:** settings, attached tunnel, and a live call list.
- **Call detail:** request and response side by side (headers, body with a JSON pretty-print toggle), the attempts timeline, and the replay, copy-as-curl and test actions.
- **No access:** what the caller is missing, with an example grant.
- **Admin:** all hooks and tunnels (metadata only), with delete.

## 11. Storage

```go
type Store interface {
    ListHooks(ctx) ([]Hook, error)
    GetHook(ctx, id) (Hook, error)
    PutHook(ctx, Hook) error            // create or replace
    DeleteHook(ctx, id) error
    BindSlug(ctx, slug string, user tailcfg.UserID) error // fails if bound to another user
    SlugOwner(ctx, slug string) (tailcfg.UserID, bool, error)
}
```

- **v1 implementation: a JSON file** at `{state-dir}/hooks.json`. The whole file is rewritten on each change (write a temp file, fsync, rename), and the file carries a `version` field for migrations. The relay keeps everything in memory and treats the file as write-through persistence.
- **Single writer:** an exclusive `flock` on the state directory stops two relays from sharing it.
- **Ephemeral hooks** are persisted too. On startup every hook counts as having no tunnel attached, so the grace timers restart. If the relay restarts during a short outage, the CLI simply attaches again.
- **Not persisted:** call logs and tunnels.

## 12. Limits and defaults

Relay-wide settings (flag / environment variable):

| Setting | Default |
|---|---|
| `--hostname` / `TSRELAY_HOSTNAME` | `tsrelay` |
| `--state-dir` / `TSRELAY_STATE_DIR` | `/var/lib/tsrelay` (binary: `$XDG_STATE_HOME/tsrelay`) |
| `--tls` / `TSRELAY_TLS` | `true` (443 via `ListenTLS`); `false` uses plain HTTP on 80 |
| `--advertise-tags` | `tag:tsrelay` |
| `--cap-name` | `mazetti.me/cap/relay` |
| `--user-slug` | `local-part` |
| `--revalidate-interval` | 60 s |
| `--max-tunnels-default` / `--max-hooks-default` | 5 / 10 per user |
| `--max-hook-ttl-default` | unlimited |
| `--ephemeral-grace` | 10 min |
| `--tunnel-concurrency` | 32 streams |
| `--hold-queue` | 100 per hook |
| `--capture-limit` / `--capture-calls` | 64 KiB / 100 per hook |
| `--capture-bodies` | `true` |
| `--redact-headers` | `Authorization,Proxy-Authorization,Cookie,Set-Cookie,X-Api-Key` |
| `--ops-listen` | empty (off) |
| `--log-format` / `--log-level` | `json` / `info` |

Per-hook defaults and maxima are listed in §8. Each per-hook setting also has a relay-wide `--hook-<setting>-default` and `--hook-<setting>-max` flag.

The Tailscale credential is `TS_AUTHKEY` (an auth key) or `TS_CLIENT_SECRET` (an OAuth client secret, recommended because auth keys expire). tsnet supports OAuth client secrets in `tailscale.com@v1.104.0`.

v1 has **no config file**. Flags and environment variables cover binaries and containers alike, and a config file is on the backlog.

## 13. Observability

**Audit log:** JSON via `log/slog` to stdout, one record per event.

| Event | Fields (in addition to time and level) |
|---|---|
| `tunnel.attach` / `.detach` / `.replaced` / `.revoked` | tunnel ID, user, node, hook IDs, reason |
| `hook.create` / `.update` / `.delete` / `.expire` | hook ID, owner, path, channel, actor (user or node), changed fields |
| `call` | call ID, hook ID, kind, sender node and tags, method, path **without the query string**, status, `Tsrelay-Error`, bytes in/out, hold/ttfb/total durations |
| `authz.deny` | caller node, user, tags, action, reason |
| `cap.parse_error` | node, parse error (logged at most once per node and value) |

**Never logged:** request or response bodies, query strings, or header values. Header *names* may appear in debug-level logs.

**Ops listener** (`--ops-listen=:9090`, off by default, **never** bound on the tailnet interface): `/healthz` (process alive), `/readyz` (tsnet up and the store loaded), `/metrics`. The shipped Kubernetes manifests enable it on the pod IP for probes.

**Metrics:** hand-written Prometheus text format, with no client library.
- `tsrelay_calls_total{outcome}`
- `tsrelay_call_bytes_total{direction}`
- `tsrelay_tunnels`
- `tsrelay_hooks{ephemeral}`
- `tsrelay_held_calls`
- `tsrelay_authz_denials_total{action}`

Histograms are on the backlog.

## 14. Deployment

- **State:** one directory holds both the tsnet state and `hooks.json`. That is a plain directory for the binary, a volume under Docker, Compose or Swarm, and a PVC on Kubernetes. Tailscale's Kubernetes Secret store (`ipn/store/kubestore`) could hold the tsnet state, but the hook store needs a volume anyway, so the default is a single PVC.
- **Replicas:** exactly one. The shipped StatefulSet uses `replicas: 1`, and the state-directory lock (§11) guards against accidents.
- **No Funnel:** the relay never calls tsnet's Funnel APIs. A test fails the build if any non-test code in the module refers to `ListenFunnel` or `FunnelOnly`.
- **TLS:** with `--tls` (the default), certificates come from tsnet and require HTTPS to be enabled for the tailnet. If it isn't, startup fails with a clear message pointing at the `--tls=false` option.
- **Shipped artifacts:**
  - static binaries for linux, darwin and windows on amd64 and arm64
  - a distroless container image
  - `deploy/kubernetes/` (StatefulSet, PVC, Secret example)
  - `deploy/compose.yaml`
  - an example policy file

## 15. CLI

```
tsrelay serve [flags]                      run the relay
tsrelay http <port|url> [--name P] [--channel C] [--persistent] [-- cmd args...]
tsrelay attach <hook>... [--forward-to <port|url>]
tsrelay hooks ls | create | update | rm
tsrelay url [<port|url>]                   print the URL tsrelay http would use
```

- **`tsrelay http 8080`** creates or reclaims an ephemeral hook, attaches a tunnel, prints the URL, and then prints one line per call:
  ```
  POST /done → 200  12ms  tag:prod-worker  https://tsrelay.…/inspect/c_7f3a
  ```
- **Targets:** a bare port means `http://localhost:<port>`. A full URL is also accepted, including a path prefix and `https://` (with `--insecure` for self-signed local certificates).
- **Stable URLs:** the CLI remembers the hook path it used for each working directory and target, in `$XDG_STATE_HOME/tsrelay/hooks.json`, and asks for the same path next time. The URL put into `.env` therefore keeps working across restarts, including after the ephemeral grace period has passed.
- **`-- cmd args...`** starts the local application with `TSRELAY_URL` set in its environment, and stops it when the tunnel command exits.
- **Finding the relay:** `--relay`, then `TSRELAY_RELAY`, then a default built from `https://tsrelay.` plus the tailnet's MagicDNS suffix, read from the local Tailscale API.
- **Output:** `--json` switches to one JSON object per event. Logs go to stderr and the URL goes to stdout, so `$(tsrelay url)` works.
- **Disconnects:** the CLI says so plainly ("tunnel lost, reconnecting… calls are being held for 30s") and reports when it has reattached.

## 16. Testing

- **No tailnet needed:** the core runs against a `net.Listener` and an `Identifier` interface:
  ```go
  type Identifier interface {
      Identify(ctx context.Context, remoteAddr string) (authz.Identity, error)
  }
  ```
  The tsnet implementation wraps `WhoIs` plus the capability parsing. The fake maps remote addresses to identities built from raw `CapMap` JSON, so tests exercise the real parser.
- **In-process end-to-end tests:** the relay, the CLI tunnel client and an `httptest` local application run in one test, all under `-race`. They cover:
  - streaming in both directions, large bodies and trailers
  - a byte-exact HMAC signature surviving the round trip
  - identity headers set, and spoofed copies stripped
  - hold-for-reconnect succeeding, and timing out
  - takeover and reject attach policies
  - revocation on revalidation
  - concurrency and rate limits
  - every `Tsrelay-Error` path
  - replay and test requests
  - CSRF and Host-header rejection
- **Unit and fuzz tests:** capability parsing and merging, channel pattern matching, slug derivation, path validation and nesting, the JSON store (crash-safe writes), and redaction.
- **Funnel guard test** (§14).
- **`testcontrol`:** during planning, check whether `tstest/integration/testcontrol` can inject app-capability grants. If it can, add one or two tests that run real tsnet nodes against it to prove the `WhoIs`→`CapMap` path. If it can't, document a manual smoke test against a real tailnet.

## 17. Backlog

- **Stores:** SQLite, Redis, Postgres, etcd.
- **Multiple replicas:** needs a shared store *and* routing calls to the replica that holds the tunnel.
- **Buffered retry and replay of in-flight calls:** opt-in per hook, buffering bodies up to a size limit (e.g. 10 MiB). Follow Svix's approach: exponential backoff with jitter, retrying only on configured statuses, and preserving the sender's idempotency headers (`webhook-id`, `Idempotency-Key`) rather than inventing new ones. Each retry is recorded as an `Attempt`.
- **Async delivery:** answer `202` and deliver in the background. Needs a disk-backed spool.
- **Capture in the CLI:** keep bodies on the developer machine only and serve them through the same inspector UI.
- **Inspector extras:** edit-and-replay, and persistent call history.
- **Escape hatch:** `senderTags` in the capability, matching tags directly.
- **Packaging:** a config file, a Helm chart, histogram metrics.

## 18. To verify during planning

1. `testcontrol` support for app-capability grants (§16).
2. Taking over the connection behind tsnet's `ListenTLS` listener, with the client forcing HTTP/1.1 through ALPN (§6.1).
3. Whether `http2.Transport.NewClientConn` can be driven over a connection whose server-side TLS has already been negotiated, without wrapping it in a second TLS layer. Expected yes: the tunnel runs HTTP/2 without TLS (h2c framing) inside the already-encrypted connection.
4. How tsnet advertises tags when it is given an OAuth client secret (`AdvertiseTags`).
