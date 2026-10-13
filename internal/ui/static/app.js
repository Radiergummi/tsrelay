// The tsrelay web UI script (DESIGN.md §10.2). Pages are rendered on the
// server. This script writes through the JSON API and keeps the parts of a
// page marked data-live current. It is an ES module so that its pure helpers
// can be tested with node (internal/ui/testdata/app_test.mjs).

// api sends a request to the relay's JSON API. It resolves to
// {ok, status, data}: data is the decoded result, or a problem document when
// ok is false.
export async function api(method, path, body) {
  const init = { method, headers: { Accept: "application/json" } };
  if (body !== undefined) {
    init.headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(body);
  }
  let resp;
  try {
    resp = await fetch(path, init);
  } catch (err) {
    return { ok: false, status: 0, data: { detail: `The relay is not reachable: ${err.message}` } };
  }
  const text = await resp.text();
  let data = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = { detail: text };
  }
  return { ok: resp.ok, status: resp.status, data };
}

// problemText says, for people, what went wrong with an API request.
export function problemText(result) {
  const p = result.data || {};
  return p.detail || p.title || `The relay answered ${result.status}.`;
}

// flash shows a message at the top of the page. An empty message hides it.
function flash(doc, text) {
  const el = doc.getElementById("flash");
  el.textContent = text;
  el.hidden = !text;
}

// eventHook returns the hook an event is about. Call events carry the
// call's hookId; hook events carry the hook's id.
export function eventHook(data) {
  try {
    const d = JSON.parse(data);
    return d.hookId || d.id || "";
  } catch {
    return "";
  }
}

// wantsEvent reports whether a data-live element shows hookID. Its
// data-hooks attribute lists hook IDs, or "*" for every hook.
export function wantsEvent(hooksAttr, hookID) {
  const ids = (hooksAttr || "").split(/\s+/).filter(Boolean);
  return ids.includes("*") || ids.includes(hookID);
}

// live keeps the page's data-live elements current. When an event concerns
// one of them, it refetches the page and swaps each data-live element for
// the fresh one with the same id. Rows are rendered only on the server.
function live(doc) {
  const parts = () => [...doc.querySelectorAll("[data-live]")];
  if (parts().length === 0) return;
  let timer = null;
  const refresh = async () => {
    timer = null;
    let resp;
    try {
      resp = await fetch(location.href, { headers: { Accept: "text/html" } });
    } catch {
      return; // the relay is unreachable; the next event or reconnect retries
    }
    if (!resp.ok) return; // e.g. the hook was deleted; keep what is shown
    const next = new DOMParser().parseFromString(await resp.text(), "text/html");
    for (const el of parts()) {
      const fresh = next.getElementById(el.id);
      if (fresh) el.replaceWith(doc.importNode(fresh, true));
    }
  };
  const schedule = () => {
    if (!timer) timer = setTimeout(refresh, 200);
  };
  const all = parts().some((el) => el.hasAttribute("data-all"));
  const events = new EventSource("/api/v1/events" + (all ? "?all=true" : ""));
  const onEvent = (e) => {
    const hook = eventHook(e.data);
    if (parts().some((el) => wantsEvent(el.dataset.hooks, hook))) schedule();
  };
  events.addEventListener("call", onEvent);
  events.addEventListener("hook", onEvent);
  // The stream ends when the relay restarts or this page falls behind. The
  // browser reconnects by itself; events may have been missed meanwhile.
  let opened = false;
  events.addEventListener("open", () => {
    if (opened) schedule();
    opened = true;
  });
}

// prettyJSON indents JSON text, or returns null if it is not valid JSON
// (for example a body cut at the capture limit).
export function prettyJSON(text) {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return null;
  }
}

// parseHeaders turns "Name: value" lines into the API's header object.
// Blank lines are skipped, and a line without a colon is a header with an
// empty value.
export function parseHeaders(text) {
  const out = {};
  for (const line of text.split("\n")) {
    const t = line.trim();
    if (!t) continue;
    const i = t.indexOf(":");
    const name = (i < 0 ? t : t.slice(0, i)).trim();
    const value = i < 0 ? "" : t.slice(i + 1).trim();
    (out[name] ??= []).push(value);
  }
  return out;
}

// actions maps a button's data-action to what clicking it does.
const actions = {
  async "delete-hook"(el, doc) {
    const r = await api("DELETE", `/api/v1/hooks/${encodeURIComponent(el.dataset.hook)}`);
    if (!r.ok) return flash(doc, problemText(r));
    location.assign(el.dataset.next || "/");
  },
  async replay(el, doc) {
    const r = await api("POST", `/api/v1/hooks/${encodeURIComponent(el.dataset.hook)}/calls/${encodeURIComponent(el.dataset.call)}/replay`);
    if (!r.ok) return flash(doc, problemText(r));
    location.assign(`/hooks/${encodeURIComponent(r.data.hookId)}/calls/${encodeURIComponent(r.data.id)}`);
  },
  "toggle-json"(el, doc) {
    const pre = doc.querySelector(el.dataset.target);
    if (pre.dataset.raw !== undefined) {
      pre.textContent = pre.dataset.raw;
      delete pre.dataset.raw;
      el.textContent = "Pretty-print";
      return;
    }
    const pretty = prettyJSON(pre.textContent);
    if (pretty === null) return flash(doc, "This body is not valid JSON, possibly because it was cut at the capture limit.");
    pre.dataset.raw = pre.textContent;
    pre.textContent = pretty;
    el.textContent = "Show raw";
  },
  async copy(el, doc) {
    const text = doc.querySelector(el.dataset.copy).textContent;
    try {
      await navigator.clipboard.writeText(text);
      flash(doc, "Copied to the clipboard.");
    } catch {
      flash(doc, "The browser did not allow copying; select the text and copy it instead.");
    }
  },
};

// clicks runs the action of any clicked element with a data-action. It
// listens on the document, so buttons in swapped data-live parts work too.
function clicks(doc) {
  doc.addEventListener("click", async (e) => {
    const el = e.target.closest("[data-action]");
    const action = el && actions[el.dataset.action];
    if (!action) return;
    e.preventDefault();
    if (el.dataset.confirm && !confirm(el.dataset.confirm)) return;
    el.disabled = true;
    try {
      await action(el, doc);
    } finally {
      el.disabled = false;
    }
  });
}

// testForm sends the hook page's test request. The API answers once the
// call is finished, which can take as long as the hook holds calls for a
// tunnel; then the page opens the call.
function testForm(doc) {
  const form = doc.getElementById("test-form");
  if (!form) return;
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const data = new FormData(form);
    const button = form.querySelector("button[type=submit]");
    button.disabled = true;
    flash(doc, "Sending… if no tunnel is attached, the relay holds the request until one is.");
    try {
      const r = await api("POST", `/api/v1/hooks/${encodeURIComponent(form.dataset.hook)}/test`, {
        method: data.get("method"),
        path: data.get("path"),
        headers: parseHeaders(data.get("headers")),
        body: data.get("body"),
      });
      if (!r.ok) return flash(doc, problemText(r));
      location.assign(`/hooks/${encodeURIComponent(r.data.hookId)}/calls/${encodeURIComponent(r.data.id)}`);
    } finally {
      button.disabled = false;
    }
  });
}

export function init(doc) {
  clicks(doc);
  live(doc);
  testForm(doc);
}

if (typeof document !== "undefined") init(document);
