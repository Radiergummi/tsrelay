import { expect, test, users } from "./fixtures";

test("the header leads to the CLI page and its script is served", async ({ page, request }) => {
  await page.goto("/");
  await page.getByRole("link", { name: "Get the CLI" }).click();
  await expect(page.getByRole("heading", { name: "Get the CLI" })).toBeVisible();
  // Playwright's desktop Chrome says it runs on Windows, so the page offers PowerShell.
  await expect(page.getByText("/install.ps1 | iex")).toBeVisible();
  await page.getByRole("tab", { name: "macOS / Linux" }).click();
  await expect(page.getByText("/install.sh | sh")).toBeVisible();
  const script = await request.get(`${users.dev}/install.sh`);
  expect(script.ok()).toBe(true);
  expect(await script.text()).toMatch(/^#!\/bin\/sh/);
});
