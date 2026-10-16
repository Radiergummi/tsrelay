import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test } from "vitest";
import { fixtures } from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";
import { CliPage } from "./CliPage";

const origin = window.location.origin;

function renderPage(version = "v0.3.0") {
  return renderWithProviders(<CliPage />, {
    route: "/cli",
    path: "/cli",
    context: { ...fixtures.me, version },
  });
}

test("offers the shell one-liner for this relay's version", () => {
  renderPage();
  expect(screen.getByText(`curl -fsSL ${origin}/install.sh | sh`)).toBeInTheDocument();
  expect(screen.getByText(/v0\.3\.0/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Copy install command" })).toBeInTheDocument();
});

test("switches to the PowerShell one-liner on the Windows tab", async () => {
  renderPage();
  await userEvent.click(screen.getByRole("tab", { name: "Windows" }));
  expect(screen.getByText(`irm ${origin}/install.ps1 | iex`)).toBeInTheDocument();
});

test("links all six builds", () => {
  renderPage();
  expect(screen.getByRole("link", { name: "macOS, Apple silicon" })).toHaveAttribute(
    "href",
    "/download/darwin/arm64",
  );
  expect(screen.getByRole("link", { name: "Windows, x86-64" })).toHaveAttribute(
    "href",
    "/download/windows/amd64",
  );
  expect(screen.getAllByRole("link", { name: /^(macOS|Linux|Windows), / })).toHaveLength(6);
});

test("says a dev relay hands out the latest release", () => {
  renderPage("dev");
  expect(screen.getByText(/latest release/)).toBeInTheDocument();
  expect(screen.getByText(/may not match this relay/)).toBeInTheDocument();
});
