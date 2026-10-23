---
title: Home
description: Deliver HTTP callbacks from your tailnet to apps running locally, without letting production connect to developer machines.
category: overview
tags: [tailscale, webhooks, callbacks, tunnel]
---

# tsrelay

Callbacks from your tailnet, delivered to localhost. tsrelay forwards HTTP callbacks from services on your tailnet to apps on developer machines, without letting production connect to them, and with nothing exposed publicly unless you opt in.

## How a call travels

Production never connects to a developer machine. Both sides connect to the relay, and the relay only forwards to hooks that someone created.

1. **Your production service** (`tag:prod-worker`) calls the hook's URL, such as `POST /~moritz/amber-otter-lamp`, like any other webhook. Its grant says which channels it may send to.
2. **The relay** (`tag:tsrelay`) is its own node on your tailnet. It checks the sender's grant, then hands the call down the tunnel.
3. **Your laptop** (`tsrelay http 8080`) dialed out to the relay and attached that tunnel, so nothing ever connects to it. Your app answers on localhost, and its response goes back to the sender.

## Why not a reverse grant, or a public tunnel?

Calls from a laptop to a tailnet service take one grant. Calls the other way have had two answers, and neither is one you'd want for production.

- **A reverse grant** gives production a standing route to every developer device it's granted, makes your policy name developer machines and keep up as they change, and ties calls to no hook and no owner, so there is nothing to inspect or replay.
- **A public tunnel**, such as [ngrok](https://ngrok.com/) or [Cloudflare Tunnel](https://developers.cloudflare.com/tunnel/), takes internal callbacks off your tailnet and across the internet, leaves who may call to the tunnel provider rather than your policy, and has each developer run and secure their own.
- **tsrelay** has laptops dial out, and lets production reach only the hooks their owners made. Traffic stays on your tailnet; a hook is public only when its owner and the operator opt in. Your grants, through [app capabilities](https://tailscale.com/docs/features/access-control/grants/grants-app-capabilities), decide who sends and receives, and every call is audited.

## What you get

- **Access from your tailnet policy.** Grants decide who may send and who may receive. There are no tokens to hand out.
- **Hooks you create, nothing else.** The relay never intercepts a service's traffic, as Telepresence or mirrord do.
- **Inspect and replay every call.** Watch calls arrive live in the web UI, read their headers and bodies, replay them, or copy one as curl.
- **Held while you reconnect.** A call waits for your tunnel to come back instead of failing, within limits you set per hook.
- **Public only when you say so.** Stripe or GitHub can reach a hook through Tailscale Funnel, once both the operator and the hook's owner opt in.
- **Audited and traced.** An audit log, Prometheus metrics and OpenTelemetry traces that span sender, relay and your app.

## Where to start

- If your tailnet already runs a relay, install the CLI from it and forward your first callback, as [Getting started](getting-started.md) shows.
- If you build against the API, the CLI's JSON or the policy grants, [Compatibility](compatibility.md) says what stays the same within a major version.
- If you run the tailnet, add the relay and grant who may send and receive, as [Running a relay](running-a-relay.md) describes.
