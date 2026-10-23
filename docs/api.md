---
title: API
description: Script the relay with the same URLs the web UI uses, as JSON or as live event streams.
category: guide
tags: [api, json, sse, openapi]
---

# API

The web UI's hook pages are also API resources at the same URLs: ask for JSON and you get the data the page shows. A `.json` or `.html` extension picks the representation; otherwise the `Accept` header does, and JSON is the default. So the URL in your browser's address bar is the one to script against:

```bash
curl https://tsrelay.<your tailnet>/hooks                               # your hooks, as JSON
curl https://tsrelay.<your tailnet>/hooks/h_3k2/calls.json              # the same as the hook page's call list
curl -H 'Accept: text/event-stream' https://tsrelay.<your tailnet>/hooks  # live changes to your hooks
```

`/` is the entry document (who you are, your limits, and links to the rest), and `/hooks/{id}` streams changes to one hook. The relay identifies you by your tailnet identity, so there are no tokens. The full description is an OpenAPI document at `/openapi.json` (or `/openapi.yaml`), which the [API reference](/api/explorer) renders.

Durations you set (`holdTimeout`, `maxHookTtl`) are Go duration strings such as `30s`; `0s` means no limit. Durations the relay measures (`durationMs`, `ttfbMs`, `waitedForMs` on calls and attempts) are JSON numbers of milliseconds with microsecond precision, so a 412 µs response reads `0.412`.

Lists are objects (`{"hooks": [...]}`, `{"calls": [...]}`), so they can gain siblings later. Within a major version the API only grows: clients must ignore unknown fields, enumeration values, event types and problem codes (falling back to the HTTP status for a code they don't know), and a request field the relay doesn't know is refused with `422` and the code `unsupported_field`. [Compatibility](compatibility.md) has the full promise.

The calls endpoints are described under [Inspecting calls](web-ui.md#inspecting-calls), and the errors the API returns in the [error reference](errors.md).
