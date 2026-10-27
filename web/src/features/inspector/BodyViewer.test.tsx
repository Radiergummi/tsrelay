import { getDefaultNormalizer, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";
import { BodyViewer } from "./BodyViewer";

const contentTypeOf = ({
  requestHeaders,
}: {
  requestHeaders?: Record<string, string[]> | undefined;
}) => requestHeaders?.["Content-Type"]?.[0];

/** textMatching finds the smallest element whose own text (joined across its children) matches pattern: Highlighted and HexView split a body's text across many sibling spans, so the usual string/regex matchers (which look at a node's own text node children) never see it whole. */
function textMatching(pattern: RegExp) {
  return (_: string, element: Element | null) => {
    if (!element || !pattern.test(element.textContent ?? "")) return false;
    return [...element.children].every((child) => !pattern.test(child.textContent ?? ""));
  };
}

test("JSON shows as a tree with exact numbers, with a raw toggle", async () => {
  const call = fixtures.callJson;
  render(
    <BodyViewer
      capture={call.requestBody}
      contentType={contentTypeOf(call)}
      metadataOnly={false}
      label="request body"
      hookId="h_1"
    />,
  );
  expect(await screen.findByText("12345678901234567890")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Raw" }));
  expect(screen.getByText(textMatching(/"amount":12345678901234567890/))).toBeInTheDocument();
});

test("a large valid JSON body is shown raw, not as a tree", () => {
  const big = JSON.stringify(Array(200_000).fill(0));
  render(
    <BodyViewer
      capture={{ body: btoa(big), size: big.length, captured: true }}
      contentType="application/json"
      metadataOnly={false}
      label="b"
      hookId="h_1"
    />,
  );
  expect(screen.getByText(/0,0,0/)).toBeInTheDocument();
  expect(screen.getByText(/too large to show as a tree, so shown raw/)).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Tree" })).not.toBeInTheDocument();
  expect(document.querySelectorAll("details")).toHaveLength(0);
});

test("a truncated body says how much was kept", () => {
  render(
    <BodyViewer
      capture={fixtures.callCut.requestBody}
      contentType="application/json"
      metadataOnly={false}
      label="request body"
      hookId="h_1"
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
      hookId="h_1"
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
      hookId="h_1"
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
      hookId="h_1"
    />,
  );
  expect(screen.getByText("No body.")).toBeInTheDocument();
  rerender(
    <BodyViewer
      capture={{ size: 0, truncated: true, captured: false }}
      contentType={undefined}
      metadataOnly={false}
      label="b"
      hookId="h_1"
    />,
  );
  expect(screen.getByText(/refused before delivery/)).toBeInTheDocument();
  rerender(
    <BodyViewer
      capture={fixtures.callAdmin.requestBody}
      contentType={undefined}
      metadataOnly
      label="b"
      hookId="h_1"
    />,
  );
  expect(screen.getByText(/only the hook's owner sees bodies/)).toBeInTheDocument();
});

test("a body not captured relay-wide doesn't point at the hook's settings", () => {
  renderWithProviders(
    <BodyViewer
      capture={{ size: 10, captured: false }}
      contentType={undefined}
      metadataOnly={false}
      relayNoBodies
      label="b"
      hookId="h_1"
    />,
  );
  expect(screen.getByText(/10 B body\. This relay doesn't capture bodies\./)).toBeInTheDocument();
  expect(screen.queryByRole("link", { name: "Edit the hook" })).not.toBeInTheDocument();
});

test("a body not captured links to the hook's settings", () => {
  renderWithProviders(
    <BodyViewer
      capture={{ size: 10, captured: false }}
      contentType={undefined}
      metadataOnly={false}
      label="b"
      hookId="h_1"
    />,
  );
  expect(
    screen.getByText(/10 B body\. Bodies aren't captured for this hook\./),
  ).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Edit the hook" })).toHaveAttribute(
    "href",
    "/hooks/h_1/edit",
  );
});
