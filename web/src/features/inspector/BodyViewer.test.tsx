import { getDefaultNormalizer, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";
import { BodyViewer } from "./BodyViewer";

const ct = (c: { requestHeaders?: Record<string, string[]> | undefined }) =>
  c.requestHeaders?.["Content-Type"]?.[0];

/** textMatching finds the smallest element whose own text (joined across its children) matches re: Highlighted and HexView split a body's text across many sibling spans, so the usual string/regex matchers (which look at a node's own text node children) never see it whole. */
function textMatching(re: RegExp) {
  return (_: string, element: Element | null) => {
    if (!element || !re.test(element.textContent ?? "")) return false;
    return [...element.children].every((child) => !re.test(child.textContent ?? ""));
  };
}

test("JSON shows as a tree with exact numbers, with a raw toggle", async () => {
  const c = fixtures.callJson;
  render(
    <BodyViewer
      capture={c.requestBody}
      contentType={ct(c)}
      metadataOnly={false}
      label="request body"
    />,
  );
  expect(await screen.findByText("12345678901234567890")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Raw" }));
  expect(screen.getByText(textMatching(/"amount":12345678901234567890/))).toBeInTheDocument();
});

test("a truncated body says how much was kept", () => {
  render(
    <BodyViewer
      capture={fixtures.callCut.requestBody}
      contentType="application/json"
      metadataOnly={false}
      label="request body"
    />,
  );
  expect(screen.getByText(/1\.0 MiB, showing the first 16 B/)).toBeInTheDocument();
});

test("binary bodies show as hex; text with NUL can be shown as text", async () => {
  render(
    <BodyViewer
      capture={fixtures.callBin.requestBody}
      contentType="application/octet-stream"
      metadataOnly={false}
      label="request body"
    />,
  );
  expect(
    await screen.findByText(/00000000 {2}89 50 4e 47/, {
      normalizer: getDefaultNormalizer({ collapseWhitespace: false }),
    }),
  ).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Show as text" })).not.toBeInTheDocument(); // not UTF-8
  render(
    <BodyViewer
      capture={{ body: btoa("a\u0000b"), size: 3, captured: true }}
      contentType="text/plain"
      metadataOnly={false}
      label="b"
    />,
  );
  await userEvent.click(screen.getByRole("button", { name: "Show as text" }));
  expect(screen.getByText(/⟨U\+0000⟩/)).toBeInTheDocument();
});

test("states without a body to show", () => {
  const { rerender } = render(
    <BodyViewer
      capture={{ size: 0, captured: true }}
      contentType={undefined}
      metadataOnly={false}
      label="b"
    />,
  );
  expect(screen.getByText("No body.")).toBeInTheDocument();
  rerender(
    <BodyViewer
      capture={{ size: 10, captured: false }}
      contentType={undefined}
      metadataOnly={false}
      label="b"
    />,
  );
  expect(screen.getByText(/10 B body, not captured: body capture is off/)).toBeInTheDocument();
  rerender(
    <BodyViewer
      capture={{ size: 0, truncated: true, captured: false }}
      contentType={undefined}
      metadataOnly={false}
      label="b"
    />,
  );
  expect(screen.getByText(/refused before delivery/)).toBeInTheDocument();
  rerender(
    <BodyViewer
      capture={fixtures.callAdmin.requestBody}
      contentType={undefined}
      metadataOnly
      label="b"
    />,
  );
  expect(screen.getByText(/only the hook's owner sees bodies/)).toBeInTheDocument();
});
