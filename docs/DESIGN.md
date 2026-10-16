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

- Public exposure, except for hooks their owners explicitly make public, served through Funnel on a separate listener (§19). Nothing else of the relay is ever reachable from the internet.
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
internal/telemetry/     OpenTelemetry setup: metrics (Prometheus, OTLP), tracing
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
  "maxHookTTL": "720h",                // receiver: cap on a hook's expiry; Go duration
  "public": true                       // receiver: may make hooks public (§19)
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
- **`public`** merges by OR: any matching value with it set to `true` grants it. It only counts on a value whose `roles` include `receiver` (§19).

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
| Attached tunnels, every `--revalidate-interval` (60 s default) | `WhoIs` again. A tunnel whose caller is no longer authorized at all is detached with reason `revoked` and closed; one that keeps some hooks but loses others only has those hooks detached, with the same `revoked` control sent naming them. A revalidation started before a takeover never undoes that takeover: it only detaches hooks the checked tunnel still serves at the moment it applies the result. |

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
- **The UI sends a strict CSP:** `default-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`, with no inline script or style, plus `X-Content-Type-Options: nosniff` and `Referrer-Policy: same-origin`. Captured headers and bodies are always rendered as text, never as HTML.

## 6. Tunnel protocol

### 6.1 Handshake

1. The client sends `GET /api/v1/tunnel?hook={id}&hook={id}…` with `Connection: Upgrade` and `Upgrade: tsrelay-tunnel/1`, using HTTP/1.1. Over TLS, the client offers only the `http/1.1` ALPN value so the connection can be taken over.
2. The relay authorizes the request (§5.5). On failure it answers with an ordinary problem response (§7.4).
3. On success the relay answers `101 Switching Protocols` and takes over the connection.
4. **The roles reverse:** the relay becomes the HTTP/2 *client* (`http2.Transport.NewClientConn`), and the CLI becomes the HTTP/2 *server* (`http2.Server.ServeConn`).

The protocol version lives in the `Upgrade` token. An incompatible change gets a new token, and the relay can accept several during a transition.

### 6.2 Streams

- **Forwarded calls:** each call attempt is one HTTP/2 request from the relay to the CLI. It carries a `Tsrelay-Hook: {id}` header, which the CLI uses to choose that hook's local target. The CLI removes all `Tsrelay-*` tunnel headers before forwarding, except the identity headers (§7.2).
- **Control messages:** requests with the header `Tsrelay-Control: 1` and a JSON body. They are handled by the CLI itself and never forwarded. Types: `attached` (the hook URLs, sent first), `replaced`, `revoked`, `hook-deleted`, `hook-updated`. Sender requests cannot forge them, because the relay strips every incoming `Tsrelay-*` header. `hook-updated` carries a sequence number `seq`. Messages are sent in the background and can arrive out of order, so a client ignores a `hook-updated` whose `seq` is not above the last it saw for that hook, and forgets that memory on each `attached` message, because the relay's sequence counter restarts with the relay.
- **Status:** the CLI reports its local target, one per tunnel, as the `target` query parameter on the handshake. The relay keeps it only if it is an `http(s)` URL without credentials, with any query string and fragment stripped, and shows it with the hook's tunnel to the owner and to admins (UI, copy-as-curl). The relay never connects to it.

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
- **`Host`** is preserved by default (`hostHeader: preserve`). Setting `rewrite` sets it to the local target's host instead, for dev servers that reject unknown hosts. The relay does not know the local target, so it signals this to the CLI with the tunnel-internal header `Tsrelay-Tunnel-Host: rewrite` on the HTTP/2 request it sends down the tunnel. A sender can never forge this: every incoming `Tsrelay-*` header is stripped first (below), and the header never reaches the local application either, because the CLI's forwarder deletes it after acting on it.
- **Added headers:** every incoming `X-Forwarded-*`, `Forwarded` and `X-Real-Ip` header is stripped, so only the connection says where a call came from. Then the relay sets `X-Forwarded-For` (the sender's tailnet IP), `X-Forwarded-Host`, `X-Forwarded-Proto` and `X-Forwarded-Prefix`. Together these let URL-signing schemes, such as Twilio's, rebuild the original URL.
- **Sender identity** is passed on to the local application. Every incoming `Tsrelay-*` header is deleted first, the same strip-then-set pattern `tailscale serve` uses (`ipn/ipnlocal/serve.go`). Then the relay sets:
  - `Tsrelay-Sender-Node`: the sender's node name
  - `Tsrelay-Sender-Tags`: its tags, comma-separated
  - `Tsrelay-Sender-Login`: set only if the sender is a user node
  - `Tsrelay-Hook`: the hook's `user/path`
  - `Tsrelay-Call-Id`: the call's ID
- **Response:** the relay adds `Tsrelay-Call-Id` to every response, so the sender's logs can be matched to the inspector.

### 7.3 Hold for reconnect

If a call arrives while no tunnel is attached to its hook, the relay waits up to `holdTimeout` (30 s by default) for one to attach before it forwards anything. Nothing has been read from the request body at that point, so streamed bodies are safe to hold. Held calls count against `--hold-queue` per hook (100 by default); beyond that, or once `holdTimeout` elapses, the call fails with `503 tunnel_unavailable` and `Retry-After: max(5s, holdTimeout)`, rounded up to whole seconds. A hook deleted, or expiring, while calls are held for it releases them at once with `404 hook_not_found`, rather than waiting out `holdTimeout`.

A call already **in flight** when its tunnel drops cannot be retried, because its body has been partly consumed. It fails with `502 tunnel_lost`. Buffered replay is on the backlog (§17).

### 7.4 Relay-originated errors

Errors produced by the relay itself use `application/problem+json` (RFC 9457) and carry a `Tsrelay-Error` header. That lets senders and developers tell them apart from responses produced by the local application, which pass through untouched, including the application's own 503s.

| `Tsrelay-Error` | Status | When |
|---|---|---|
| `sender_forbidden` | 403 | Caller has no `sender` role at all |
| `hook_not_found` | 404 | No such hook, or the sender's channels don't match it. The two cases are deliberately indistinguishable to the sender; the owner sees channel-denied calls in the inspector. |
| `body_too_large` | 413 | Over `maxBodyBytes` (unlimited by default) |
| `rate_limited` | 429 + `Retry-After` | Hook rate limit exceeded. `Retry-After` is the limiter's wait, rounded up to whole seconds, at least 1. |
| `tunnel_unavailable` | 503 + `Retry-After` | No tunnel attached within `holdTimeout`, or the hold queue is full. `Retry-After` is `max(5s, holdTimeout)`, rounded up to whole seconds. |
| `tunnel_overloaded` | 503 + `Retry-After: 1` | Per-tunnel concurrency limit reached |
| `upstream_unreachable` | 502 | The CLI could not connect to the local target. The CLI reports this as a 502 with its own `Tsrelay-Error`, which the relay passes through. |
| `tunnel_lost` | 502 | Tunnel dropped mid-call |
| `upstream_timeout` | 504 | No response headers within `responseTimeout`, or the body was idle for `idleTimeout` |

A call whose sender went away before it completed is recorded with the outcome `sender_canceled` (audit `error`, `tsrelay_calls_total{outcome}`). Nobody receives it, so it is not a `Tsrelay-Error` code.

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
| `capture` | `true` | Capture bodies for the inspector. Metadata is always recorded. Relay-wide default `--hook-capture-default`; `--capture-bodies=false` turns capture off for every hook. |
| `public` | `false` | Reachable from the internet at `/+{token}`; needs the `public` grant and `--funnel` (§19) |

## 9. Inspector

### 9.1 Data model

```
Call     { id, hookID, receivedAt, kind: sender|replay|test, sender: {node, tags, login, ip},
           method, path, query, queryTruncated, requestHeaders, redacted, requestBody: Capture,
           outcome, status, duration, attempts: []Attempt }
Attempt  { id, startedAt, tunnelID, node, waitedFor (hold), ttfb, duration,
           status, responseHeaders, redacted, responseBody: Capture,
           bytesIn, bytesOut, error: Tsrelay-Error code or "" }
Capture  { bytes (≤ captureLimit), size, truncated, captured }
```

- `path` is the escaped path below the hook's route, as the local app sees it. `query` is kept up to `--capture-limit` bytes; `queryTruncated` says it was cut.
- `redacted` (on the call and on each attempt) lists header names left out either because they are redacted or because the headers exceeded `--capture-limit`.
- `status` and `duration` on the call are what the sender received and how long the call took.
- `Capture.truncated` means the bytes are not the whole body: it was longer than `--capture-limit`, or was never read to the end, as for a call refused before delivery. `Capture.captured` is false when body capture was off.
- `outcome` is `pending` while the call is delivered, then `ok` (delivered, whatever the local app answered), a `Tsrelay-Error` code, or `sender_canceled`.
- An attempt starts when delivery starts, holding included. A call refused before that (rate limit, body limit, channel) has no attempts.

Every call has an attempts list from the start, even though v1 only ever creates one attempt per call. Replays create a new call of kind `replay` that refers to the original. Buffered retries (backlog) will add further attempts to the same call.

### 9.2 Storage and visibility

- **Retention:** the last 100 calls per hook (configurable), in memory, gone on restart. Each captured body is held up to 64 KiB (configurable). Calls that were denied by channel are recorded on the hook they targeted, so the owner can see them.
- **Visibility:** only the hook's owner sees query strings, headers and bodies. Admins see metadata only.
- **Redaction:** `Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie` and `X-Api-Key` are redacted by default. The list is configurable relay-wide. Redacted values are dropped at capture time and never held in memory.
- **Global switch:** operators can turn body capture off for the whole relay with `--capture-bodies=false`.

### 9.3 Actions

- **Replay:** re-sends a captured call down the hook's current tunnel, for calls whose request body was captured in full. Redacted headers are left out. The response goes only to the inspector, never back to the original sender. Only the owner can replay. The `Tsrelay-Sender-*` headers name the original sender, because a replay reproduces that call. A replayed public call carries `Tsrelay-Sender-Public: true` again, has its trace headers stripped, and names the hook's public route in `X-Forwarded-Prefix` while the hook is still public (its tailnet route otherwise). A call whose request body or query string was not captured in full gets `409`. A call refused by channel was never delivered either, so it also gets `409`.
- **Copy as curl:** two variants. One targets the hook URL, which only works for nodes with the sender role. The other targets the tunnel's local target (e.g. `http://localhost:8080/…`), which is what developers usually want.
- **Send test request:** the owner writes a request (method, path, headers, body; prefilled with a small JSON sample) and sends it through the normal pipeline as a call of kind `test`. The sender authorization step is skipped, because the owner is calling their own hook. This proves the whole chain works on first setup. The API takes `{method, path, headers, body}`. `method` defaults to `POST`, `path` to `/`, and `path` may include a query string. The sender is the owner. Replay and test both answer `201` with the finished call; admins get `403`.

## 10. Web UI and API

### 10.1 JSON API (`/api/v1`)

| Method & path | Purpose |
|---|---|
| `GET /me` | Identity, effective grant (roles, channel patterns, limits), current usage, user slug, the relay's capability name (`capName`), the relay-wide setting defaults and maxima, and how many calls it keeps per hook (`policy`, with `callsPerHook`) |
| `GET /hooks` | The caller's hooks (admins: `?all=true`). Every hook object, here and elsewhere, carries `lastCall`: its newest call's summary. |
| `POST /hooks` | Create. `?dryRun=true` validates without saving and answers `200` with the would-be hook. |
| `GET/PATCH/DELETE /hooks/{id}` | Read, update or delete. `PATCH` also accepts `?dryRun=true`, which validates without saving and answers `200` with the would-be hook. |
| `GET /hooks/{id}/calls` | Call summaries, newest first |
| `GET /hooks/{id}/calls/{callID}` | Full call with attempts and captures. For the hook's owner only, `replay: {allowed, reason}` says whether it can be replayed and why not. |
| `POST /hooks/{id}/calls/{callID}/replay` | Replay. Answers `201` with the new call as `GET` shows it to its owner, `replay` included. |
| `POST /hooks/{id}/test` | Send a test request. Answers like a replay. |
| `GET /events` | Server-sent events: `call` (a call summary, on arrival and on completion) and `hook` (`{"id"}` of a hook that changed, or whose tunnel did). Admins may add `?all=true`. A stream that falls 64 events behind is closed; clients reconnect and refetch. |
| `GET /tunnel` | Tunnel upgrade (§6) |

- **Hook IDs** are short, opaque and random. Hook paths contain slashes, so they can't serve as IDs.
- **Validation errors** are `422` problem responses with a per-field `errors` list. A field error that comes from a grant names the limiting rule, for example "channel `billing` is not matched by your receiver patterns `prod`, `staging.*`".

The CLI uses this same API.

### 10.2 UI

The UI is a React single-page app in `web/`, built with Vite and styled with Tailwind, and embedded in the binary from `web/dist` (`make ui`). A binary built without it (a plain `go build`) serves a placeholder page that says how to build it. Screens:

- **Hooks:** your hooks, with path, channel, kind, tunnel (attached node and local target), last call, expiry and URL.
- **Create / edit hook:** the settings in §8. Every change is checked with a dry run, and the relay's errors appear under their fields. Each setting shows the relay default and maximum from `/me`; the channel picker is fed by the receiver patterns.
- **Hook:** a split pane. The call list (live, newest first, filtered in the browser by status class, method, path and sender, with the filters in the URL) is on the left, and the inspector is on the right: the request and the response (headers with redacted ones marked, bodies as a JSON tree, highlighted text or hex), the attempts, a diff of a replay against its original, and replay and copy-as-curl. Below about 900px the panes stack. Keyboard: `j`/`k`, `Enter`/`Esc`, `/`, `r`, `c`, `t`, `?`.
- **No access:** what the caller is missing, with an example grant.
- **Admin:** all hooks and tunnels (metadata only), with delete.

Data flow: the app reads and writes only through the JSON API (§10.1), so `Guard` protects it like any other client, and the server decides visibility: owners get a call's query, headers and bodies; admins get metadata only. The UI hides owner-only controls when the viewer isn't the owner, but the API enforces it.

Live updates: one `EventSource` per tab on `/api/v1/events` (with `?all=true` on the admin page and on another user's hook), closed while the tab is hidden. `call` events update the cached call list (trimmed to `policy.callsPerHook`, as the relay keeps no more) and the hook's last call and refresh an open call; `hook` events refetch the hook, the lists and `/me` (its hook count). After a reconnect (with backoff) the app refetches what it shows, because events may have been missed.

Theme: light, dark or the system's, stored in the browser and applied before the first paint by `/theme.js`.

CSP (§5.7): every UI response carries `default-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`. There is no inline script or style; the theme script is a file, and Base UI is told not to render `<style>` elements. Captured data is only ever rendered as text: the highlighting comes from the app's own tokenizer, invisible and bidi control characters are shown as `⟨U+XXXX⟩`, and only the hook's URL and the relay's own routes are links. CI fails on any HTML sink in the app's source and on any CSP violation in the end-to-end tests.

Routes: `/`, `/hooks/new`, `/hooks/{id}`, `/hooks/{id}/edit`, `/hooks/{id}/calls/{callID}`, `/admin`, and `/inspect/{callID}`, which redirects to the call on the server (the CLI links here) or shows that the call has expired. Every other path that isn't a built file gets the app (for browsers) or a 404 problem. An admin without the receiver role starts on `/admin`.

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
| `--hook-capture-default` | `true` |
| `--ops-listen` | empty (off) |
| `--ops-pprof` | `false` |
| `--otel` | `false` (OTLP/HTTP, configured by `OTEL_*`) |
| `--trace-propagation` | `true` |
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

**Ops listener** (`--ops-listen=:9090`, off by default, **never** bound on the tailnet interface): `/healthz` (process alive), `/readyz` (tsnet up and the store loaded), `/metrics`. The shipped Kubernetes manifests enable it on the pod IP for probes. With `--ops-pprof` (off by default) it also serves `net/http/pprof` under `/debug/pprof/`; the shipped manifests leave it off.

**Metrics:** OpenTelemetry instruments, served as Prometheus text on the ops listener's `/metrics` (and pushed over OTLP with `--otel`, §13.1). The exporter keeps the names below exactly: no unit suffixes, no `otel_scope_*` labels, no `target_info`. Counters and histograms are derived from audit events through an `audit.Logger` observer hook, so the request path gets no second instrumentation layer; gauges are read from the tunnel registry and the hook manager at collection time.
- `tsrelay_calls_total{outcome}`
- `tsrelay_call_bytes_total{direction}`
- `tsrelay_tunnels`
- `tsrelay_hooks{ephemeral}`
- `tsrelay_held_calls`
- `tsrelay_authz_denials_total{action}`
- `tsrelay_call_duration_seconds{outcome,kind}`: arrival to the last byte back
- `tsrelay_call_ttfb_seconds{outcome,kind}`: forwarding to the app's response headers, only when they arrived
- `tsrelay_call_hold_seconds{outcome}`: time held calls waited for a tunnel
- `tsrelay_api_request_duration_seconds{method,route,status}`: JSON API requests by route pattern; the event stream and tunnel attaches are excluded
- Go runtime metrics (`go_*`)

Histogram buckets, in seconds: 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120. Labels never carry hook IDs, users, paths or nodes.

### 13.1 Tracing

With `--otel`, the relay records OpenTelemetry spans and exports them, and its metrics, over OTLP/HTTP, configured by the standard `OTEL_*` variables. No instrumentation library is used; every attribute is set explicitly, so no query string, header value or body can reach a span.

- `tsrelay.call` (server): the whole call, response streaming included. Attributes mirror the `call` audit record. Its status is an error only when the relay produced a `Tsrelay-Error`; the app's own 5xx is just a status code. Events: `routed`, `channel_denied`, `body_too_large`, `rate_limited`.
- `tsrelay.authorize`: identifying the sender (WhoIs); skipped for replays and test requests.
- `tsrelay.hold`: waiting for a tunnel, with `tsrelay.hold.result` (`attached`, `timeout`, `queue_full`, `hook_gone`, `canceled`).
- `tsrelay.forward` (client): sending the call down the tunnel until the app's response headers.
- `tsrelay.tunnel.attach` (server): authorization, upgrade, handshake and registration, with `tsrelay.tunnel.result` (`attached` or `refused`), the actor and the hook IDs.
- `tsrelay.tunnel.detach`: a short span in its own trace, linked to the attach, with `tsrelay.tunnel.reason` (`disconnected`, `orphaned`, `revoked`, `shutdown`) and the tunnel's lifetime.
- API requests (server): one span per request, named after the route pattern (`PATCH /api/v1/hooks/{id}`), with the actor's login. Replays and test requests are children of their API request's span. The event stream and tunnel attaches get no request span.

With `--trace-propagation` (the default), a sender's call joins the sender's trace, and `traceparent`/`tracestate` sent to the app are replaced by `tsrelay.forward`'s. With `false`, or without `--otel`, they pass through untouched. With tracing off no span code runs at all.

## 14. Deployment

- **State:** one directory holds both the tsnet state and `hooks.json`. That is a plain directory for the binary, a volume under Docker, Compose or Swarm, and a PVC on Kubernetes. Tailscale's Kubernetes Secret store (`ipn/store/kubestore`) could hold the tsnet state, but the hook store needs a volume anyway, so the default is a single PVC.
- **Replicas:** exactly one. The shipped StatefulSet uses `replicas: 1`, and the state-directory lock (§11) guards against accidents.
- **Funnel is confined:** only `cmd/tsrelay/funnel.go` may call tsnet's Funnel APIs, and a test fails the build otherwise. The public listener's handler serves nothing but `/+…` (§19).
- **TLS:** with `--tls` (the default), certificates come from tsnet and require HTTPS to be enabled for the tailnet. If it isn't, startup fails with a clear message pointing at the `--tls=false` option.
- **Shipped artifacts:**
  - static binaries for linux, darwin and windows on amd64 and arm64
  - a distroless container image
  - `deploy/kubernetes/` (StatefulSet, PVC, Secret example)
  - `deploy/compose.yaml`
  - an example policy file

## 15. CLI

```
tsrelay http <port|url> [flags] [-- cmd args...]   forward a new or remembered hook to a local app
tsrelay attach <hook>... -t/--to <port|url>       serve existing hooks
tsrelay hooks list (alias ls)                     list your hooks
tsrelay hooks show <hook>                         one hook in detail
tsrelay hooks create [<path>] [flags]
tsrelay hooks update <hook> [flags]
tsrelay hooks delete <hook>... (alias rm)
tsrelay url [<port|url>]                          print the URL `http` would use
tsrelay serve [flags]                             run the relay
tsrelay completion bash|zsh|fish|powershell       cobra's generated completion
tsrelay --version                                 fang; there is no `version` subcommand
```

`<hook>` is a hook ID or path; `resolveHooks` looks it up among the caller's hooks.

- **`tsrelay http 8080`** creates or reclaims an ephemeral hook, attaches a tunnel, prints the URL, and then prints one line per call. On a terminal, a call line has the time, method, path, status, duration and sender in aligned columns, and ends in the call ID as an OSC 8 hyperlink to `<relay>/inspect/<callID>`. Plain output prints the full link. With `--json`, call events carry `callId` and `inspect`.
- **Flags** are GNU style, with shorthands where they read naturally: `-n`, `-c`, `-p`, `-k` on `http`; `-t`, `-k` on `attach`; `-j` everywhere. `serve` has none.
- **The environment:** every flag `--foo-bar` can be set as `TSRELAY_FOO_BAR`, and the command line wins.
- **Targets:** a bare port means `http://localhost:<port>`. A full URL is also accepted, including a path prefix and `https://` (with `--insecure` for self-signed local certificates).
- **Stable URLs:** the CLI remembers the hook path it used for each working directory and target, in `$XDG_STATE_HOME/tsrelay/hooks.json`, and asks for the same path next time. The URL put into `.env` therefore keeps working across restarts, including after the ephemeral grace period has passed.
- **`-- cmd args...`** starts the local application with `TSRELAY_URL` set in its environment, and stops it when the tunnel command exits.
- **Finding the relay:** `--relay`, then `TSRELAY_RELAY`, then a default built from `https://tsrelay.` plus the tailnet's MagicDNS suffix, read from the local Tailscale API.
- **Output:**
  - Hook URLs go to stdout and everything else to stderr, so `$(tsrelay url)` works.
  - Each stream is styled only when it's a terminal with colors on (`NO_COLOR` and `--no-color` turn colors off).
  - `--json` prints JSON on stdout: indented on a terminal, compact otherwise. Errors go to stderr as JSON.
- **Errors:** relay refusals show their title, detail and, for common codes, a hint.
- **Exit codes:** 2 for usage errors (including bad `TSRELAY_*` values), 1 for failures, and the child's own code when the command after `--` exits.
- **Disconnects:** the CLI says so plainly ("tunnel lost, reconnecting… calls are held for 30s") and reports when it has reattached ("reconnected").

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
- **Funnel confinement and public-surface tests** (§14, §19).
- **`testcontrol`:** during planning, check whether `tstest/integration/testcontrol` can inject app-capability grants. If it can, add one or two tests that run real tsnet nodes against it to prove the `WhoIs`→`CapMap` path. If it can't, document a manual smoke test against a real tailnet.
- **Performance:** benchmarks for a call through the whole relay (with and without tracing) against the app called directly, the tunnel's framing, and the inspector's capture and redaction; CI runs each once so they keep working. `TestLoad` (gated by `TSRELAY_LOAD=1`) is an in-process load harness reporting the latency the relay adds. `docs/perf/baseline.md` holds the reference numbers.

## 17. Backlog

- **Stores:** SQLite, Redis, Postgres, etcd.
- **Multiple replicas:** needs a shared store *and* routing calls to the replica that holds the tunnel.
- **Buffered retry and replay of in-flight calls:** opt-in per hook, buffering bodies up to a size limit (e.g. 10 MiB). Follow Svix's approach: exponential backoff with jitter, retrying only on configured statuses, and preserving the sender's idempotency headers (`webhook-id`, `Idempotency-Key`) rather than inventing new ones. Each retry is recorded as an `Attempt`.
- **Async delivery:** answer `202` and deliver in the background. Needs a disk-backed spool.
- **Capture in the CLI:** keep bodies on the developer machine only and serve them through the same inspector UI.
- **Inspector extras:** edit-and-replay, and persistent call history.
- **Escape hatch:** `senderTags` in the capability, matching tags directly.
- **Packaging:** a config file, a Helm chart.

## 18. To verify during planning

1. `testcontrol` support for app-capability grants (§16).
2. Taking over the connection behind tsnet's `ListenTLS` listener, with the client forcing HTTP/1.1 through ALPN (§6.1).
3. Whether `http2.Transport.NewClientConn` can be driven over a connection whose server-side TLS has already been negotiated, without wrapping it in a second TLS layer. Expected yes: the tunnel runs HTTP/2 without TLS (h2c framing) inside the already-encrypted connection.
4. How tsnet advertises tags when it is given an OAuth client secret (`AdvertiseTags`).

## 19. Public hooks

A hook can be made reachable from the internet through Tailscale Funnel, opt-in at two levels: the operator runs the relay with `--funnel`, and the hook's owner sets it `public`. Everything else — the UI, the API, `/~user/hook` routes and static files — stays tailnet-only. The relay does no sender authentication for public calls; apps are expected to verify the provider's signature themselves, which works because bodies are forwarded byte-exact (§7.2), as third-party webhooks (Stripe, GitHub, Twilio) already assume.

**Configuration:** `--funnel` (default `false`) opens the public listener; `--funnel-port` (default `443`, or `8443`/`10000`, the only ports Funnel supports) and `--funnel-max-body-bytes` (default 25 MiB, GitHub's webhook maximum) bound it. `--funnel` requires `--tls`. Startup fails with a clear message if the policy denies the node the `funnel` node attribute for this port, or if Funnel is requested without HTTPS enabled. Whether a user may mark their own hooks public is a policy decision: a `"public": true` field on a receiver grant value (§5.2), merged by OR and meaningless without `"roles": ["receiver"]` (§5.3).

**URL and token:** `https://<relay MagicDNS name>[:port]/+{token}/{remainder...}`, the port omitted at 443; `{remainder}` and the query string are forwarded exactly as for tailnet hooks (§4), and `/+` mirrors `/~`. Each public hook carries a `PublicToken`: 16 bytes of `crypto/rand`, base64url, 22 characters. It is generated when `public` goes false→true, cleared on true→false, and replaced on an explicit rotate (`POST /api/v1/hooks/{id}/rotate-public-url`, or `tsrelay hooks rotate-url`). The owner can always see it; it never appears in audit records, spans or metric labels.

**Listener:** `cmd/tsrelay/funnel.go` is the only non-test file allowed to call `ListenFunnel`/`FunnelOnly` (enforced by `TestFunnelConfined`). It opens a Funnel-only listener served by its own `http.Server`, sharing no router with the tailnet side. Its `ConnContext` (`funnelConnContext`) only records the source address of a genuine `*ipn.FunnelConn`, when the connection is one; it does not reject anything itself. The handler wrapper `withFunnelSource` is what fails closed: it aborts any request whose context lacks that recorded address, with `http.ErrAbortHandler` — which `FunnelOnly` should make unreachable, but the abort is the actual enforcement, not `ConnContext`. Shutting the listener down cleanly clears the node's `AllowFunnel` serve-config entry, but tsnet persists that entry otherwise: a crash, or a restart without `--funnel`, can leave the node still showing Funnel allowed even though nothing is listening. The relay is still safe (no listener means no traffic reaches it), but `--funnel=false` is not by itself proof that Tailscale has stopped advertising the node as Funnel-capable.

**Request handling:** `PublicHandler()` answers a plain `404` for every path that doesn't start with `/+`, revealing nothing about what else the relay serves. A public call runs the ordinary pipeline (§7.1) with a pre-built anonymous sender, routed by its token — scanning hooks linearly, like `Route` does, rather than through a separate index. An unknown token, or one whose hook isn't currently public, fails routing outright: it gets the same `404 hook_not_found` as any other missing hook, before the body-size or rate-limit checks and without ever looking for a tunnel. A hook that is public but whose owner has since lost the `public` grant routes normally — its body cap (`min(--funnel-max-body-bytes, hook.maxBodyBytes)`, `413`) and rate limit (`429`) are checked like any call — and only turns into that same `404` once a tunnel is in hand, already attached or after a successful hold; if the hold times out instead, the caller gets the ordinary `503 tunnel_unavailable`. The relay strips every incoming `Tsrelay-*`, `X-Forwarded-*`, `Forwarded`, `Traceparent` and `Tracestate` header and sets `Tsrelay-Sender-Public: true`, `Tsrelay-Hook`, `Tsrelay-Call-Id` and its own `X-Forwarded-*` headers; there is no `Tsrelay-Sender-Node/Tags/Login`, since there is no tailnet identity. The hook's rate-limit bucket (`rateLimit`, §8) is shared between its public and tailnet callers, so a public token holder can exhaust the same bucket a tailnet sender relies on.

**Revocation:** the tunnel revalidator (§5.5) also refreshes whether each tunnel's owner still holds the `public` grant, so losing it stops public delivery within one `--revalidate-interval`, the same as any other revocation. Gaining it takes effect sooner: when the owner makes a hook public, or updates one that is, the relay identifies the node of the hook's tunnel again and lets it receive public calls if that node now holds the grant. The caller's grants never decide this, since the caller may be on another node. The hook's `public` flag and token are left alone, so restoring the grant brings the same URL back.

**API and CLI:** hook JSON gains `public` (visible to anyone who can see the hook) and `publicUrl` (owner only, and only while the hook is public — admins see `public` but never the URL). `GET /api/v1/me` gains `public` (the caller's own grant) and `funnel` (a bool: whether this relay serves Funnel at all). `tsrelay http`, `hooks create` and `hooks update` take `--public`; `tsrelay hooks rotate-url <hook>` issues a new token. Granting `public` without the policy field is a `422` field error on `public`, the same shape as any other disallowed setting, not a `403`.
