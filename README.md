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
14:02:11 POST /payments/done → 200  12ms  tag:prod-worker
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

## Errors

Errors produced by the relay itself are `application/problem+json` responses and carry a `Tsrelay-Error` header. Responses from your app are never marked, including its own 5xx.

| `Tsrelay-Error` | Status | Meaning |
|---|---|---|
| `sender_forbidden` | 403 | The caller has no sender grant |
| `hook_not_found` | 404 | No such hook, or not in the caller's channels |
| `tunnel_unavailable` | 503 | No tunnel attached; retry later |
| `tunnel_overloaded` | 503 | Too many concurrent calls on the tunnel |
| `upstream_unreachable` | 502 | The tunnel is up but your app is not listening |
| `tunnel_lost` | 502 | The tunnel dropped mid-call |
| `upstream_timeout` | 504 | Your app did not answer within `--response-timeout` |

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
| `--ephemeral-grace` | `10m` | How long a hook survives without its tunnel |
| `--response-timeout` | `2m` | |

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
go test -race ./...                                # everything except the tailnet test
go test -race -tags tailnet ./internal/server      # real tsnet nodes against an in-process control server
```

The design is in [docs/DESIGN.md](docs/DESIGN.md).
