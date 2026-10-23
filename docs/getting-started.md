---
title: Getting started
description: Install the tsrelay CLI from your relay and forward your first callback to an app on your machine.
category: guide
tags: [installation, quickstart, cli]
---

# Getting started

This is for you if someone on your tailnet already runs a relay and has granted you access. To set up a relay, see [Running a relay](running-a-relay.md).

Tailscale must be running and logged in on your machine, and your tailnet must have [MagicDNS](https://tailscale.com/kb/1081/magicdns) on. The CLI asks the local Tailscale for your tailnet's name to find the relay at `https://tsrelay.<your tailnet>`, and reaches it over your machine's Tailscale connection, so the relay knows who you are. If your relay has another name, pass `--relay` or set `TSRELAY_RELAY`.

## Install the CLI

Install the CLI from your relay: open its web UI and choose **Get the CLI**, or run

```bash tab="macOS, Linux"
curl -fsSL https://tsrelay.<your tailnet>/install.sh | sh
```

```powershell tab="Windows"
irm https://tsrelay.<your tailnet>/install.ps1 | iex
```

You get the version your relay runs. When the two are different major versions, the CLI warns and suggests [`tsrelay update`](#updating-the-cli).

## Forward a hook

Forward a hook to your local app:

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

Next, learn what a [hook](hooks.md) can do, or open the relay's [web UI](web-ui.md) to watch calls arrive.

## Updating the CLI

The CLI should run the same major version as your relay; a CLI and a relay of the same major version work together, whichever is newer. `tsrelay update` asks the relay for its version, downloads that release's binary for your platform and its SHA-256 checksum from GitHub, checks the one against the other, and replaces the running executable in place:

```bash
tsrelay update           # asks first; --yes doesn't
tsrelay update --check   # only says whether the versions differ; exits 4 if the relay is newer
tsrelay update --force   # also installs the relay's version when yours is newer
```

If the executable's directory isn't writable, for example because a package manager installed it there, `update` changes nothing and prints the commands to use instead. `update` refuses to replace a newer tsrelay with the relay's older version unless you pass `--force`. A build from source (`tsrelay --version` says `dev`) doesn't update itself.

When the CLI notices that the relay runs a newer version, it asks `Relay is v1.1.0, you have v1.0.0. Update now? [Y/n]` once the command is done, unless you interrupted it. It asks at most once a day, and only on a terminal: never with `--json`, in a pipe, during completion, or in a background tunnel. A relay that is older than your CLI is never mentioned, except in a warning when it is another major version. Relay admins are also told, at most once a day, when a newer tsrelay release is out than the relay runs.

`update` doesn't check the release's Sigstore attestations. To verify those, download the binary and run `gh attestation verify` as in [Verifying a download](https://tsrelay.mazetti.me/download#verifying-a-download).
