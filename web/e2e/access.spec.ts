import { expect, test, users } from "./fixtures";

test("a user without a role sees the no-access page with a grant", async ({ page }) => {
  await page.goto(`${users.nobody}/`);
  await expect(page.getByText(/grants you no access to this relay/)).toBeVisible();
  await expect(page.locator("pre")).toContainText('"nobody@example.com"');
  await expect(page.locator("pre")).toContainText('"tsrelay.mazetti.me/cap/relay"');
});

test("the CLI's inspect links redirect, and expired ones say so", async ({ page }) => {
  await page.goto("/inspect/c_json");
  await expect(page).toHaveURL(/\/hooks\/[^/]+\/calls\/c_json$/);
  const resp = await page.goto("/inspect/c_gone");
  expect(resp?.status()).toBe(404);
  await expect(page.getByText("This call has expired from the inspector")).toBeVisible();
});

test("unknown pages", async ({ page }) => {
  await page.goto("/no/such/page");
  await expect(page.getByText("There is no page here.")).toBeVisible();
});
