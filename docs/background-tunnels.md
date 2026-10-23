---
title: Background tunnels
description: Keep a tunnel running without holding on to a terminal, and list, follow and stop the tunnels you started.
category: guide
tags: [tunnels, background, cli]
---

# Background tunnels

`--background` (on `http` and `attach`) gives you the terminal back once the tunnel is attached. tsrelay prints the hook URL and an ID for the tunnel, which keeps running on its own:

```bash
$ tsrelay http 8080 --background
https://tsrelay.example.ts.net/~moritz/amber-otter-lamp
running in the background as k3x9qa
tsrelay logs k3x9qa -f  ·  tsrelay stop k3x9qa
```

If the tunnel can't attach, you get the error as you would in the foreground, and nothing is left running.

```bash
tsrelay ps                                         # background tunnels: ID, hooks, target, uptime, state
tsrelay logs <id|hook> [-f]                        # print a tunnel's log, or follow it
tsrelay stop <id|hook>... | --all                  # stop tunnels
```

With `--json`, `http --background` prints the tunnel as one object once it is attached, `ps` prints `{"runs": [...]}` and `stop` prints `{"stopped": [...], "failed": [...]}`; each run is `{"id", "pid", "command", "relay", "target", "hooks", "startedAt", "state"}`, described in [Command line](cli.md#json-output).

A tunnel is named by its ID or by a hook it serves. `stop` gives each tunnel 10 s to detach cleanly, then kills it and says so. A tunnel whose process has ended, say because its hook was deleted, shows up once in `ps` as `exited` and is then forgotten, log included; read its log before that with `tsrelay logs`.

Each tunnel keeps its record and log in `$XDG_STATE_HOME/tsrelay/run/` (`~/.local/state/tsrelay/run/` by default). Background tunnels don't survive a reboot or logout, and aren't restarted when they fail; use your system's service manager for that. `tsrelay http --background -- cmd` is refused: start the command yourself, for example `TSRELAY_URL=$(tsrelay url 8080) npm run dev`.
