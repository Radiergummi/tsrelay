---
title: Running a relay
description: Add a tsrelay relay to your tailnet, deploy it as a binary, container or on Kubernetes, and serve public hooks.
category: guide
tags: [operators, deployment, kubernetes, docker, funnel]
---

# Running a relay

This is for operators: whoever adds the relay to the tailnet and decides who may use it. Before the relay is of use to anyone, grant access to it in your tailnet policy, as [Access control](access-control.md) describes.

## Run the relay

Enable [HTTPS certificates](https://tailscale.com/kb/1153/enabling-https) for your tailnet, or run the relay with `--tls=false` and grant `tcp:80` instead of `tcp:443`.

```bash
TS_CLIENT_SECRET=tskey-client-… tsrelay serve
```

Use an OAuth client secret with the `tag:tsrelay` tag. Auth keys (`TS_AUTHKEY`) also work, but they expire. State lives in `--state-dir`: `/var/lib/tsrelay` in containers, `~/.local/state/tsrelay` otherwise. Run exactly one relay per state directory.

The relay serves the web UI, the API and the CLI downloads on its own URL, so users need nothing else: point them at [Getting started](getting-started.md).

## Deployment

Ship it as:

- a static binary (`go build ./cmd/tsrelay`, or [download one](/download))
- a container image, `ghcr.io/radiergummi/tsrelay` (see the [`Dockerfile`](https://github.com/Radiergummi/tsrelay/blob/main/Dockerfile))
- Kubernetes, with [`deploy/kubernetes/`](https://github.com/Radiergummi/tsrelay/tree/main/deploy/kubernetes) (a `StatefulSet` with `replicas: 1`, a `Secret` example, no `Service`: the relay is reached over the tailnet only)
- Docker Compose, with [`deploy/compose.yaml`](https://github.com/Radiergummi/tsrelay/blob/main/deploy/compose.yaml)

Run exactly one relay per state directory (`--state-dir`); the [design document](https://github.com/Radiergummi/tsrelay/blob/main/docs/DESIGN.md) explains why in §11 and §14.

## Serving public hooks

To let users make hooks reachable from the internet (see [Public hooks](hooks.md#public-hooks)), run the relay with `--funnel`. The tailnet policy also needs the `funnel` node attribute for the relay, and `"public": true` on the grants of the users who may make hooks public — see [`deploy/policy.example.hujson`](https://github.com/Radiergummi/tsrelay/blob/main/deploy/policy.example.hujson) for both.

```bash
tsrelay serve --funnel          # serve public hooks through Tailscale Funnel
```

Every other setting is in the [configuration reference](configuration.md).
