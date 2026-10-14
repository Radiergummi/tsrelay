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
  await page.getByLabel("Hold timeout").fill("soon");
  await expect(page.getByText("must be a duration like 30s, 2m or 1h30m")).toBeVisible();
  await page.getByLabel("Hold timeout").fill("45s");
  await page.getByRole("button", { name: "Create hook" }).click();
  await expect(page.getByRole("heading", { level: 1, name })).toBeVisible();
  await expect(page.getByText(`Created ${name}`)).toBeVisible();
  await api(request).deleteHook(await api(request).hookId(name));
});
