import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { expect, test } from "vitest";
import type { LicenseComponent } from "@/api/schemas";
import { renderWithProviders } from "@/test/render";
import { server } from "@/test/server";
import { LicensesPage } from "./LicensesPage";

function renderPage(components: LicenseComponent[]) {
  server.use(http.get("/licenses", () => HttpResponse.json({ components })));
  return renderWithProviders(<LicensesPage />, { route: "/licenses", path: "/licenses" });
}

const sample: LicenseComponent[] = [
  { name: "alpha", ecosystem: "npm", licenses: [{ id: "MIT" }] },
  { name: "beta", ecosystem: "npm", licenses: [{ id: "Apache-2.0" }] },
  { name: "gamma", ecosystem: "go", licenses: [{ id: "MIT" }] },
];

const chip = (name: RegExp) => screen.getByRole("button", { name });
const licenseSelect = () => screen.getByRole("combobox", { name: "License" });
const options = () =>
  within(licenseSelect())
    .getAllByRole("option")
    .map((o) => o.textContent);

test("filters by ecosystem", async () => {
  renderPage(sample);
  expect(await screen.findByText("gamma")).toBeInTheDocument();
  await userEvent.click(chip(/^npm/));
  expect(screen.queryByText("gamma")).not.toBeInTheDocument();
  expect(screen.getByText("alpha")).toBeInTheDocument();
  expect(screen.getByText("beta")).toBeInTheDocument();
});

test("filters by name", async () => {
  renderPage(sample);
  await userEvent.type(await screen.findByPlaceholderText("Search by name…"), "alp");
  expect(screen.getByText("alpha")).toBeInTheDocument();
  expect(screen.queryByText("beta")).not.toBeInTheDocument();
  expect(screen.queryByText("gamma")).not.toBeInTheDocument();
});

test("combines the license filter with the search", async () => {
  renderPage([
    { name: "alpha", ecosystem: "npm", licenses: [{ id: "MIT" }] },
    { name: "alpine", ecosystem: "npm", licenses: [{ id: "Apache-2.0" }] },
    { name: "beta", ecosystem: "npm", licenses: [{ id: "MIT" }] },
  ]);
  await screen.findByText("alpha");
  await userEvent.selectOptions(licenseSelect(), "MIT");
  await userEvent.type(screen.getByPlaceholderText("Search by name…"), "alp");
  // alpine matches the search but not the license, beta the reverse.
  expect(screen.getByText("alpha")).toBeInTheDocument();
  expect(screen.queryByText("alpine")).not.toBeInTheDocument();
  expect(screen.queryByText("beta")).not.toBeInTheDocument();
});

test("counts each ecosystem chip against the other filters, not itself", async () => {
  renderPage(sample);
  await screen.findByText("alpha");
  expect(chip(/^All\s*3$/)).toBeInTheDocument();
  expect(chip(/^Go\s*1$/)).toBeInTheDocument();
  expect(chip(/^npm\s*2$/)).toBeInTheDocument();

  await userEvent.selectOptions(licenseSelect(), "MIT");
  expect(chip(/^All\s*2$/)).toBeInTheDocument();
  expect(chip(/^Go\s*1$/)).toBeInTheDocument();
  expect(chip(/^npm\s*1$/)).toBeInTheDocument();

  // Picking an ecosystem must not shrink the chips' own counts, or they
  // could not be used to switch between ecosystems.
  await userEvent.click(chip(/^npm\s*1$/));
  expect(chip(/^Go\s*1$/)).toBeInTheDocument();
  expect(chip(/^All\s*2$/)).toBeInTheDocument();
});

test("counts the license options against the ecosystem and the search", async () => {
  renderPage(sample);
  await screen.findByText("alpha");
  expect(options()).toEqual(["All licenses (3)", "Apache-2.0 (1)", "MIT (2)"]);
  await userEvent.click(chip(/^Go/));
  // Apache-2.0 would show nothing, so it is not offered.
  expect(options()).toEqual(["All licenses (1)", "MIT (1)"]);
});

test("opens the license text and the notice from a license badge", async () => {
  server.use(
    http.get("/licenses/:id", ({ params }) =>
      HttpResponse.text(params.id === "t1" ? "MIT License body" : "NOTICE body"),
    ),
  );
  renderPage([
    { name: "alpha", ecosystem: "npm", licenses: [{ id: "MIT" }], textId: "t1", noticeId: "n1" },
  ]);
  await userEvent.click(await screen.findByRole("button", { name: "MIT" }));
  const dialog = await screen.findByRole("dialog");
  expect(await within(dialog).findByText("MIT License body")).toBeInTheDocument();
  expect(within(dialog).getByText("NOTICE body")).toBeInTheDocument();
  expect(within(dialog).getByRole("heading", { name: "NOTICE" })).toBeInTheDocument();
});

test("a license without a text is not a button", async () => {
  renderPage([{ name: "alpha", ecosystem: "npm", licenses: [{ id: "MIT" }] }]);
  expect(await screen.findByText("MIT")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "MIT" })).not.toBeInTheDocument();
});

test("says when a text does not load", async () => {
  server.use(
    http.get("/licenses/:id", () =>
      HttpResponse.json({ title: "Not Found", status: 404 }, { status: 404 }),
    ),
  );
  renderPage([{ name: "alpha", ecosystem: "npm", licenses: [{ id: "MIT" }], textId: "gone" }]);
  await userEvent.click(await screen.findByRole("button", { name: "MIT" }));
  expect(await screen.findByText("Could not load the license text.")).toBeInTheDocument();
});

test("links only http(s) URLs, after turning git URLs into browser ones", async () => {
  renderPage([
    {
      name: "safe",
      ecosystem: "npm",
      licenses: [{ id: "MIT" }],
      homepage: "https://example.com/safe",
      repository: "git+https://github.com/example/safe.git",
    },
    {
      name: "hostile",
      ecosystem: "npm",
      licenses: [{ id: "MIT" }],
      homepage: ["javascript", "alert(1)"].join(":"),
      repository: "data:text/html,<script>alert(1)</script>",
    },
  ]);
  expect(await screen.findByRole("link", { name: "safe" })).toHaveAttribute(
    "href",
    "https://example.com/safe",
  );
  expect(screen.getByRole("link", { name: "Source" })).toHaveAttribute(
    "href",
    "https://github.com/example/safe",
  );
  expect(screen.queryByRole("link", { name: "hostile" })).not.toBeInTheDocument();
  expect(screen.getByText("hostile")).toBeInTheDocument();
  expect(screen.getAllByRole("link", { name: "Source" })).toHaveLength(1);
});

test("offers all notices for download", async () => {
  renderPage(sample);
  expect(await screen.findByRole("link", { name: "Download all notices" })).toHaveAttribute(
    "href",
    "/notices.txt",
  );
});
