---
title: Running a relay
description: Add a tsrelay relay to your tailnet, deploy it as a binary, container or on Kubernetes, size, back up and upgrade it, and serve public hooks.
category: guide
tags: [operators, deployment, kubernetes, docker, funnel]
---

# Running a relay

This is for operators: whoever adds the relay to the tailnet and decides who may use it. Before the relay is of use to anyone, grant access to it in your tailnet policy, as [Access control](access-control.md) describes. Read the [security model](security-model.md) too: it says what the relay protects and what it leaves to you.

## Run the relay

Enable [HTTPS certificates](https://tailscale.com/kb/1153/enabling-https) for your tailnet, or run the relay with `--tls=false` and grant `tcp:80` instead of `tcp:443`.

The relay joins your tailnet with an [OAuth client](https://tailscale.com/kb/1215/oauth-clients) secret. Create the client in the Tailscale admin console with the `auth_keys` write scope, and let it assign `tag:tsrelay`, the tag the relay advertises (`--advertise-tags`). Then start the relay:

```bash
TS_CLIENT_SECRET='tskey-client-…?ephemeral=false&preauthorized=true' tsrelay serve
```

The relay uses the secret to create an auth key for itself. Keep the `?ephemeral=false&preauthorized=true` suffix: without it, that key is ephemeral and not pre-authorized. The tailnet removes an ephemeral device once it has been offline for a while, so a relay that was stopped can come back as a new device. A key that isn't pre-authorized leaves the relay waiting for approval if your tailnet requires [device approval](https://tailscale.com/kb/1099/device-approval).

The credential is only used to join the tailnet. Once joined, the relay keeps its node key in the state directory and ignores the credential, and tagged devices have key expiry off by default. An auth key in `TS_AUTHKEY` also works; that it expires only matters when the relay has to join again because its state is gone, and then an OAuth client secret saves you from creating a new key by hand.

State lives in `--state-dir`: `/var/lib/tsrelay` in containers, `~/.local/state/tsrelay` otherwise. The relay locks it, so exactly one relay runs per state directory.

The relay serves the web UI, the API and the CLI downloads on its own URL, so users need nothing else: point them at [Getting started](getting-started.md).

## Deployment

Ship it as:

- a static binary (`go build ./cmd/tsrelay`, or [download one](https://tsrelay.mazetti.me/download))
- a container image, `ghcr.io/radiergummi/tsrelay` (see the [`Dockerfile`](https://github.com/Radiergummi/tsrelay/blob/main/Dockerfile)). It runs as user 65532, and writes only to its state directory.
- Kubernetes, with [`deploy/kubernetes/`](https://github.com/Radiergummi/tsrelay/tree/main/deploy/kubernetes)
- Docker Compose, with [`deploy/compose.yaml`](https://github.com/Radiergummi/tsrelay/blob/main/deploy/compose.yaml): `TS_CLIENT_SECRET=… docker compose -f deploy/compose.yaml up -d`

Pin the image to an exact version, such as `ghcr.io/radiergummi/tsrelay:1.0.0`, or better, to its digest. The `:1` tag moves to every new 1.x release.

### Kubernetes

The manifests are a `ServiceAccount`, a `Secret` example and a `StatefulSet` with `replicas: 1`. There is no `Service`: the relay is reached over the tailnet only. The manifests set no `namespace`, so pass one:

```bash
kubectl create namespace tsrelay
kubectl apply -n tsrelay -f deploy/kubernetes/service-account.yaml
kubectl apply -n tsrelay -f secret.yaml          # your copy of secret.example.yaml, with the secret filled in
kubectl apply -n tsrelay -f deploy/kubernetes/statefulset.yaml
```

The pod meets the Pod Security `restricted` profile: it runs as user 65532 with a read-only root file system, no privilege escalation and no capabilities, and doesn't mount a Kubernetes API token. Its state is a 1 GiB volume claim. The ops listener (`/healthz`, `/readyz`, `/metrics`) is on the pod IP, port 9090, for the probes and for scraping; it has no authentication, so keep it from other workloads with a `NetworkPolicy` if your cluster runs workloads you don't trust.

## How senders reach the relay

A sender has to be a device on your tailnet: the relay identifies each caller by its tailnet identity, and grants decide what it may do. A production service gets there in one of these ways:

- It embeds Tailscale itself, for example with [tsnet](https://tailscale.com/kb/1244/tsnet).
- It runs next to a Tailscale container or sidecar, which joins the tailnet with the service's tags.
- On Kubernetes, it calls through an egress proxy of the [Tailscale Kubernetes operator](https://tailscale.com/kb/1236/kubernetes-operator). The relay then sees the proxy's device, not the pod: the sender's identity, the `Tsrelay-Sender-*` headers and the grant that applies are the proxy's tags. Every pod that can use the proxy can send as it. The request still has to name the relay by its MagicDNS name, `tsrelay.<your tailnet>`, for TLS and because the relay refuses other host names with `421`.

## Serving public hooks

To let users make hooks reachable from the internet (see [Public hooks](hooks.md#public-hooks)), run the relay with `--funnel`. The tailnet policy also needs the `funnel` node attribute for the relay, and `"public": true` on the grants of the users who may make hooks public; [`deploy/policy.example.hujson`](https://github.com/Radiergummi/tsrelay/blob/main/deploy/policy.example.hujson) shows both.

```bash
tsrelay serve --funnel          # serve public hooks through Tailscale Funnel
```

## Sizing

The relay's memory is mostly the inspector's: the most recent calls of each hook, kept in memory. `--capture-total-bytes` (128 MiB) bounds what it keeps of all calls together; past it, the oldest calls of any hook go first. Senders can't push the relay past it however many hooks there are or whatever they send, so size the container for it:

```text
memory limit ≈ 2 × --capture-total-bytes + 128 MiB
```

The shipped manifest's 512 MiB limit fits the default with room to spare. The factor of 2 covers the garbage collector's headroom; the 128 MiB covers the rest of the relay, including calls in flight, each of which buffers up to `--capture-limit-bytes` of each body while it runs, for up to `--tunnel-concurrency` calls per tunnel.

Within the budget, each hook keeps up to `--capture-calls` calls (100) and, apart from those, up to 10 calls refused by channel. Of each call the inspector keeps the path (at most 8 KiB), and up to `--capture-limit-bytes` (64 KiB) each of the query string, the request headers and the response headers. Only for hooks that capture bodies does it also keep up to 64 KiB of each body. Capture is off for hooks unless their owner or `--hook-capture-default` turns it on.

To keep more history, raise `--capture-total-bytes` and the memory limit together. To keep less, or to keep no bodies at all:

- `--capture-bodies=false` keeps no bodies, whatever the hooks say.
- A lower `--capture-limit-bytes` shrinks what each call keeps; a lower `--capture-calls` keeps fewer calls per hook.

On Kubernetes, the manifest sets `GOMEMLIMIT` to about 90% of the container's 512 MiB memory limit, so Go collects garbage harder before the kernel would kill the container; change both together.

## Backups

All of the relay's state is in its state directory:

- `hooks.json`: the hooks, with their settings and public URLs, and which user each `~slug` belongs to.
- `tsnet/`: the relay's tailnet identity (its node key) and TLS certificates.

Nothing else needs a backup: captured calls and tunnels live only in memory. Copy the directory while the relay is stopped, or snapshot its volume. Restore it only where the original no longer runs: two relays with the same node key fight over one tailnet device.

## Restarts

When the relay gets `SIGTERM` (or Ctrl-C), it stops in this order:

1. It stops accepting connections, and answers calls held for a tunnel at once with `503 shutting_down` and `Retry-After: 5`. Event streams in the web UI end.
2. Calls already delivered to a tunnel get up to `--shutdown-timeout` (20 s) to finish. Keep it below the time your supervisor waits before killing the process: on Kubernetes, the pod's `terminationGracePeriodSeconds`, 30 s by default.
3. It closes the tunnels. A call still running then fails with `563 tunnel_lost`.

A restart keeps every hook, ephemeral ones included; an ephemeral hook's grace period starts again. The CLI reconnects on its own and attaches its hooks again, waiting up to 30 s between attempts. What a restart loses:

- Calls that arrive while the relay is down. They fail to connect. Senders have to retry these and the `503`s; tsrelay never retries a call itself.
- The inspector's call history.

## Upgrades and rollback

Within a major version, the CLI and the relay work together whichever is newer, as [Compatibility](compatibility.md) describes, so upgrade the relay first and let users catch up with `tsrelay update`.

`hooks.json` records its store format. A relay refuses to start on a store written in a newer format than it reads, so a rollback works only as long as the store format didn't change in between: 0.6.0 and 1.0.0 both use format 1. Copy the state directory before an upgrade if you may want to go back.

## Node failures

The relay runs as one replica. There is no high availability: while it's down, calls fail. On Kubernetes, the `StatefulSet` restarts a pod that crashes. If the node the pod runs on fails, though, the pod stays `Terminating`, and no replacement starts, until that node comes back or you force-delete the pod (`kubectl delete pod -n tsrelay tsrelay-0 --force --grace-period=0`). Do that only once you're sure the old pod no longer runs, since the state directory's lock only works within one machine.

## Alerts

These use the metrics on the ops listener; [Observability](observability.md#metrics) lists them all. `outcome` is `ok` or the relay's error code.

| Alert | Query | Means |
|---|---|---|
| Relay down | `up == 0` for the relay's scrape job, for 5 minutes | Nothing is delivered. Check the pod and its logs |
| Hold queues full | `sum(rate(tsrelay_calls_total{outcome="hold_queue_full"}[5m])) > 0` for 15 minutes | Calls are refused for hooks without a tunnel, because many are already waiting |
| Tunnels overloaded | `sum(rate(tsrelay_calls_total{outcome="tunnel_overloaded"}[5m])) > 0` for 15 minutes | A tunnel has more calls in flight than `--tunnel-concurrency` |
| Tunnels lost | `sum(rate(tsrelay_calls_total{outcome="tunnel_lost"}[5m]))` well above its usual rate | Tunnels drop mid-call, for many users at once if the relay's own connection is the problem |
| Senders refused | `sum(rate(tsrelay_authz_denials_total{action="call"}[5m]))` well above its usual rate | Often a policy change that shut out a sender |
| Memory | the container's working set above 80% of its limit | Lower the capture limits, or raise the memory limit |

Don't alert on `tunnel_unavailable` (561): it mostly means a laptop is asleep.

## When senders see 56x

Senders see these statuses for failures between the relay and an app; [Errors](errors.md) explains each from the sender's side. As an operator:

- **561 `tunnel_unavailable`**: no tunnel was attached in time. Normal for one hook; for many at once, check `tsrelay_tunnels`. If it fell to zero, the relay restarted or lost its tailnet connection, or a policy change revoked receivers: look for `tunnel.revoked` records in the [audit log](observability.md#audit-log).
- **562 `hold_queue_full`**: more calls are waiting for one hook's tunnel than `--hook-hold-queue` allows. Usually a sender retrying fast against a hook whose owner is offline; ask the owner to attach or delete the hook.
- **562 `tunnel_overloaded`**: a tunnel has `--tunnel-concurrency` calls in flight. The owner's app is slow, or the limit is too low for the traffic.
- **563 `tunnel_lost`**: a tunnel dropped mid-call. For one hook, the owner's laptop went away; for many at once, check the relay's logs for a restart or connection trouble.
- **564 `upstream_unreachable`** and **565 `upstream_timeout`**: the tunnel worked, but the owner's app didn't listen or answer in time. Nothing for you to fix; the owner can raise the hook's `responseTimeout` up to `--hook-response-timeout-max`.

Every other setting is in the [configuration reference](configuration.md).
