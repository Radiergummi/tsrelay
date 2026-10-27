import { render, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";
import { BodyViewer } from "./BodyViewer";
import { HeaderTable } from "./HeaderTable";

/** encode base64-encodes text's UTF-8 bytes in chunks: spreading a long Uint8Array into String.fromCharCode overflows the call stack. */
const encode = (text: string) => {
  const bytes = new TextEncoder().encode(text);
  let binary = "";
  for (let i = 0; i < bytes.length; i += 0x8000)
    binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(binary);
};
const captureOf = (text: string) => ({
  body: encode(text),
  size: new TextEncoder().encode(text).length,
  captured: true,
});

/** inert fails if the rendered capture produced any element or URL the capture asked for. */
function inert(container: HTMLElement) {
  expect(
    container.querySelectorAll("script, img, iframe, object, embed, a, form, style, link"),
  ).toHaveLength(0);
  for (const element of container.querySelectorAll("*")) {
    for (const attribute of element.getAttributeNames())
      expect(attribute.startsWith("on")).toBe(false);
  }
}

/** settled waits for BodyViewer's lazily loaded views (JsonTree, HexView) to finish mounting, so inert() inspects their real output instead of the Suspense skeleton. */
async function settled(container: HTMLElement) {
  await waitFor(() => {
    expect(container.querySelector('[data-slot="skeleton"]')).not.toBeInTheDocument();
  });
}

/** Each case names the toggle buttons to click, in order, to visit every view the capture offers — not a wildcard search, so a renamed or missing toggle fails the test instead of being silently skipped. */
const bodies: [string, string, string, string[]][] = [
  ["html", "text/html", fixtures.hostileHtml.requestBody.body!, []],
  ["json", "application/json", fixtures.hostileJson.requestBody.body!, ["Raw"]],
  ["form", "application/x-www-form-urlencoded", fixtures.hostileForm.requestBody.body!, []],
  [
    "json with markup",
    "application/json",
    encode(
      `{"<script>alert(1)</script>": "javascript:alert(2)", "x": "<img src=x onerror=alert(3)>"}`,
    ),
    ["Raw"],
  ],
  ["text", "text/plain", encode("<script>alert(4)</script>\u202egnp.exe"), []],
  [
    "text with a NUL",
    "text/plain",
    encode("<script>alert(5)</script>\u0000\u202egnp.exe"),
    ["Show as text"],
  ],
];

test.each(bodies)(
  "%s bodies render as literal text in every view",
  async (_, contentType, body, toggles) => {
    const capture = { body, size: atob(body).length, captured: true };
    const { container, getByRole, queryAllByRole } = render(
      <BodyViewer
        capture={capture}
        contentType={contentType}
        metadataOnly={false}
        label="body"
        hookId="h_1"
      />,
    );
    await settled(container);
    inert(container);
    // A single-view capture offers no toggle at all: check that explicitly instead of letting an empty `toggles` list skip the check.
    const unexpectedToggles =
      toggles.length === 0
        ? queryAllByRole("button", { name: /^(Raw|Tree|Show as text|Hex)$/ })
        : [];
    expect(unexpectedToggles).toHaveLength(0);
    for (const name of toggles) {
      await userEvent.click(getByRole("button", { name }));
      await settled(container);
      inert(container);
    }
    const text = container.textContent ?? "";
    expect(text).not.toContain("\u202e");
    expect(text).toMatch(/<script>alert\(\d\)<\/script>|<img src=x onerror=alert\(\d\)>/);
  },
);

test("bidi controls are shown, not obeyed", () => {
  const { container } = render(
    <BodyViewer
      capture={captureOf("abc\u202edef")}
      contentType="text/plain"
      metadataOnly={false}
      label="b"
      hookId="h_1"
    />,
  );
  expect(container.textContent).toContain("abc⟨U+202E⟩def");
});

test("adjacent control characters merge into one revealed run", async () => {
  const { container, getByRole } = render(
    <BodyViewer
      capture={captureOf("<script>alert(5)</script>\u0000\u202egnp.exe")}
      contentType="text/plain"
      metadataOnly={false}
      label="b"
      hookId="h_1"
    />,
  );
  await userEvent.click(getByRole("button", { name: "Show as text" }));
  expect(container.textContent).toContain("⟨U+0000⟩⟨U+202E⟩");
});

test("a very long single line stays plain text and renders", () => {
  const long = "A".repeat(300 * 1024);
  const { container } = render(
    <BodyViewer
      capture={captureOf(long)}
      contentType="application/json"
      metadataOnly={false}
      label="b"
      hookId="h_1"
    />,
  );
  expect(container.textContent).toContain(long.slice(0, 1000));
  inert(container);
});

test("headers are text too", () => {
  const call = fixtures.hostileHtml;
  const { container } = render(
    <HeaderTable headers={call.requestHeaders} redacted={["Authorization"]} metadataOnly={false} />,
  );
  inert(container);
  expect(container.textContent).toContain("</td><script>alert(6)</script>");
  expect(container.textContent).toContain("abc⟨U+202E⟩def");
  expect(container.textContent).toMatch(/Authorization.*left out/);
});
