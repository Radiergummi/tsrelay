import { api, CSP, expect, test } from "./fixtures";

test("the hooks list shows tunnels and updates the last call live", async ({ page, request }) => {
  const resp = await page.goto("/");
  expect(resp?.headers()["content-security-policy"]).toBe(CSP);
  const row = page
    .getByRole("row")
    .filter({ has: page.getByRole("link", { name: "payments/done" }) });
  await expect(row).toContainText("dev-laptop");
  const id = await api(request).hookId("payments/done");
  await api(request).sendTest(id, "/e2e-live");
  await expect(row).toContainText("/e2e-live"); // calls record the path below the hook's URL
});
