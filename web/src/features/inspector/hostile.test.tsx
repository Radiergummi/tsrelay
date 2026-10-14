import { render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";
import { BodyViewer } from "./BodyViewer";
import { HeaderTable } from "./HeaderTable";

/** enc base64-encodes s's UTF-8 bytes in chunks: spreading a long Uint8Array into String.fromCharCode overflows the call stack. */
const enc = (s: string) => {
  const bytes = new TextEncoder().encode(s);
  let bin = "";
  for (let i = 0; i < bytes.length; i += 0x8000)
    bin += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(bin);
};
const cap = (s: string) => ({
  body: enc(s),
  size: new TextEncoder().encode(s).length,
  captured: true,
});

/** inert fails if the rendered capture produced any element or URL the capture asked for. */
function inert(container: HTMLElement) {
  expect(
    container.querySelectorAll("script, img, iframe, object, embed, a, form, style, link"),
  ).toHaveLength(0);
  for (const el of container.querySelectorAll("*")) {
    for (const attr of el.getAttributeNames()) expect(attr.startsWith("on")).toBe(false);
  }
}

const bodies: [string, string, string][] = [
  ["html", "text/html", fixtures.hostileHtml.requestBody.body!],
  ["json", "application/json", fixtures.hostileJson.requestBody.body!],
  ["form", "application/x-www-form-urlencoded", fixtures.hostileForm.requestBody.body!],
  [
    "json with markup",
    "application/json",
    enc(
      `{"<script>alert(1)</script>": "javascript:alert(2)", "x": "<img src=x onerror=alert(3)>"}`,
    ),
  ],
  ["text", "text/plain", enc("<script>alert(4)</script>\u202egnp.exe")],
];

test.each(bodies)(
  "%s bodies render as literal text in every view",
  async (_, contentType, body) => {
    const capture = { body, size: atob(body).length, captured: true };
    const { container, findAllByRole } = render(
      <BodyViewer capture={capture} contentType={contentType} metadataOnly={false} label="body" />,
    );
    inert(container);
    for (const toggle of await findAllByRole("button", {
      name: /^(Raw|Tree|Show as text|Hex)$/,
    }).catch(() => [])) {
      await userEvent.click(toggle);
      inert(container);
    }
    const text = container.textContent ?? "";
    expect(text).not.toContain("\u202e");
    // form values are split on "=" and "&", so only html/json/text bodies keep the script tag literal.
    if (contentType !== "application/x-www-form-urlencoded") {
      // eslint-disable-next-line vitest/no-conditional-expect -- intentional: see comment above.
      expect(text).toMatch(/<script>alert\(\d\)<\/script>|<img src=x onerror=alert\(\d\)>/);
    }
  },
);

test("bidi controls are shown, not obeyed", () => {
  const { container } = render(
    <BodyViewer
      capture={cap("abc\u202edef")}
      contentType="text/plain"
      metadataOnly={false}
      label="b"
    />,
  );
  expect(container.textContent).toContain("abc⟨U+202E⟩def");
});

test("a very long single line stays plain text and renders", () => {
  const long = "A".repeat(300 * 1024);
  const { container } = render(
    <BodyViewer
      capture={cap(long)}
      contentType="application/json"
      metadataOnly={false}
      label="b"
    />,
  );
  expect(container.textContent).toContain(long.slice(0, 1000));
  inert(container);
});

test("headers are text too", () => {
  const h = fixtures.hostileHtml;
  const { container } = render(
    <HeaderTable headers={h.requestHeaders} redacted={["Authorization"]} metadataOnly={false} />,
  );
  inert(container);
  expect(container.textContent).toContain("</td><script>alert(6)</script>");
  expect(container.textContent).toContain("abc⟨U+202E⟩def");
  expect(container.textContent).toMatch(/Authorization.*left out/);
});
