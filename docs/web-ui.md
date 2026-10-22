---
title: Web UI and inspector
description: Manage hooks in the browser, watch calls arrive, and inspect, replay or test them.
category: guide
tags: [web ui, inspector, replay]
---

# Web UI and inspector

## Web UI

Open the relay's URL (`https://tsrelay.<your tailnet>/`) in a browser on your tailnet. You see your hooks with their tunnels and latest calls, and can create, edit and delete hooks. The hook form shows each delivery setting at the relay default until you change it, takes the expiry in your local time, and keeps a preview of the resulting URL and effective settings beside it. Each hook's page shows its calls as they arrive, and lets you send it a test request. A call's page shows the request and the response side by side, and lets you replay the call or copy it as a curl command (aimed at your local target or at the hook URL). Each call line `tsrelay http` prints ends with a link to that call.

Each hook's page has the call list on the left and the selected call on the right; the list updates live, and you can filter it by status, method, path and sender. Keyboard shortcuts: `j`/`k` move through the calls, `Enter`/`Esc` select and deselect, `/` focuses the filter, `r` replays, `c` copies as curl, `t` sends a test request, and `?` lists them. The UI needs JavaScript; the CLI and the [API](api.md) don't.

## Inspecting calls

The relay keeps the most recent calls of each hook in memory (100 unless your operator changed it): method, path, sender, status and timings, the query and the headers (minus redacted ones such as `Authorization`), and the start of each body if the hook captures bodies, which it doesn't unless you or your operator turn capture on. Only you see your hooks' queries, headers and bodies; admins see metadata only. Calls are lost when the relay restarts.

| | |
|---|---|
| `GET /hooks/{id}/calls` | Call summaries, newest first; with `Accept: text/event-stream`, each call as it arrives and completes |
| `GET /hooks/{id}/calls/{callID}` | One call with its captured headers and bodies |
| `POST /hooks/{id}/calls/{callID}/replay` | Send the call again; its body and query string must have been captured in full |
| `POST /hooks/{id}/test` | Send `{"method","path","headers","body"}` through the relay to your app |

Replays and test requests go through the whole relay to your app. The answer is recorded in the inspector and returned by the API, never sent to the original sender. Every response a sender gets carries `Tsrelay-Call-Id`, which names the call here.
