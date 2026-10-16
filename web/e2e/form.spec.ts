import { api, expect, test } from "./fixtures";

test("creating a hook shows dry-run errors under their fields, then saves", async ({
  page,
  request,
}) => {
  await page.goto("/hooks/new");
  const path = page.getByLabel("Path");
  await path.fill("not a valid path");
  await expect(path).toHaveAttribute("aria-invalid", "true");
  const name = `e2e-${test.info().project.name}-${Date.now()}`;
  await path.fill(name);
  await expect(path).not.toHaveAttribute("aria-invalid", "true");
  await page.getByRole("button", { name: /Delivery/ }).click();
  await page
    .getByRole("radiogroup", { name: "Hold timeout" })
    .getByRole("radio", { name: "custom…" })
    .click();
  const hold = page.getByLabel("Hold timeout, custom");
  await hold.fill("soon");
  await expect(page.getByText("must be a duration like 30s, 2m or 1h30m")).toBeVisible();
  await hold.fill("45s");
  await expect(hold).not.toHaveAttribute("aria-invalid", "true");
  await page.getByRole("button", { name: "Create hook" }).click();
  await expect(page.getByRole("heading", { level: 1, name })).toBeVisible();
  await expect(page.getByText(`Created ${name}`)).toBeVisible();
  await api(request).deleteHook(await api(request).hookId(name));
});

test("making a hook public shows its internet URL", async ({ page, request }) => {
  await page.goto("/hooks/new");
  const name = `e2e-public-${test.info().project.name}-${Date.now()}`;
  await page.getByLabel("Path").fill(name);
  await page.getByRole("radio", { name: "Tailnet and internet" }).click();
  await page.getByRole("button", { name: "Create hook" }).click();
  await expect(page.getByRole("heading", { level: 1, name })).toBeVisible();
  await expect(page.getByText(/^https:\/\/tsrelay\.example\.ts\.net\/\+/)).toBeVisible();
  await api(request).deleteHook(await api(request).hookId(name));
});
