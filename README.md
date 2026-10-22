# tsrelay

Deliver HTTP callbacks from your tailnet to apps running locally, without letting production connect to developer machines, and with nothing exposed publicly unless you opt in.

Production services call the relay, which is its own tailnet node. Developer devices dial *out* to the relay and attach a tunnel. The relay forwards each callback down the tunnel to your local app and returns your app's response. Access is controlled entirely by your tailnet policy.

<img width="936" height="648" alt="Graph depicting the call chain between a tailnet service and a local device" src="https://github.com/user-attachments/assets/dbacfb85-0221-4700-a9ac-2bf28610b504" />

tsrelay is deliberately narrow. It forwards only to hooks you create explicitly. It never intercepts services as Telepresence or mirrord do, and it is reachable from the internet only for hooks their owners explicitly make public, through Tailscale Funnel, when the operator runs it with `--funnel`.

## Why

Communication from a local device and a tailnet service is easy: you grant a user group permission to connect to a tag, and it works.
The reverse, however, is more complicated: while you can obviously just create the reverse policy and allow a tag to connect to all devices of a user, or a whole user group, that opens up a lot of security issues, isn't auditable, and grants any service in prod a permanent backdoor into your endpoints.
It also encodes details about your local developer's environments into your ACL policy, which is annoying to maintain and keep track of.  
To avoid all of this, you could reach for a tool like [ngrok](https://ngrok.com/) or [Cloudflare Tunnel](https://developers.cloudflare.com/tunnel/), but that tunnels internal traffic via the internet.

Instead, you can also set up tsrelay: it adds a tiny relay service to your tailnet that can be configured using standard ACLs and [app capabilities](https://tailscale.com/docs/features/access-control/grants/grants-app-capabilities) to control who can send and receive callbacks in your tailnet.
Users can set up hooks that are fully configurable, can be routed to multiple user devices, buffer requests on unreliable lines, have a full request log, include Tailscale authorization headers, and more. In turn, the relay service itself has a full audit log, OpenTelemetry traces and metrics, as well as global control over all hooks.

A web interface lets users manage their hooks and view the request log, which also offers manual calls, replays, and access to all details. Everything updates live.

## Quick start

If someone on your tailnet already runs a relay, install the CLI from it and forward a hook to your local app:

```bash
curl -fsSL https://tsrelay.<your tailnet>/install.sh | sh
tsrelay http 8080
```

To run a relay, grant access in your tailnet policy and start it with an OAuth client secret tagged `tag:tsrelay`:

```bash
TS_CLIENT_SECRET=tskey-client-… tsrelay serve
```

## Documentation

Full documentation is at <https://tsrelay.mazetti.me>, and in [`docs/`](docs) in this repository:

- Using tsrelay: [getting started](docs/getting-started.md), [hooks](docs/hooks.md), [background tunnels](docs/background-tunnels.md), [receiving calls](docs/receiving-calls.md), [web UI and inspector](docs/web-ui.md), [API](docs/api.md) and the [command line](docs/cli.md).
- Running a relay: [running a relay](docs/running-a-relay.md), [access control](docs/access-control.md) and [observability](docs/observability.md).
- Reference: [configuration](docs/configuration.md) and [errors](docs/errors.md).

Binaries for macOS, Linux and Windows, and the container image, are on the [download page](https://tsrelay.mazetti.me/download).

# Contributing

Building, testing and releasing tsrelay is described in [CONTRIBUTING.md](CONTRIBUTING.md), and its design in [docs/DESIGN.md](docs/DESIGN.md). The website is in [`website/`](website) and builds its pages from [`docs/`](docs).

# License

Apache License 2.0; see [LICENSE](LICENSE) and [NOTICE](NOTICE).
