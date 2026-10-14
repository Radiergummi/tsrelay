import { api, expect, test } from "./fixtures";

test.beforeEach(async ({ context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
});

test("the viewers show JSON, truncated, binary and refused calls", async ({ page, request }) => {
  const id = await api(request).hookId("payments/done");
  await page.goto(`/hooks/${id}/calls/c_json`);
  await expect(page.getByText("12345678901234567890")).toBeVisible();
  await page.goto(`/hooks/${id}/calls/c_cut`);
  await expect(page.getByText(/showing the first/)).toBeVisible();
  await page.goto(`/hooks/${id}/calls/c_bin`);
  await expect(page.getByText(/00000000 {2}89 50 4e 47/)).toBeVisible();
  await page.goto(`/hooks/${id}/calls/c_refused`);
  await page.getByRole("tab", { name: "Attempts" }).click();
  await expect(page.getByText(/No attempts/)).toBeVisible();
});

test("copy as curl", async ({ page, request }) => {
  const id = await api(request).hookId("payments/done");
  await page.goto(`/hooks/${id}/calls/c_json`);
  await page.getByRole("button", { name: "Copy as curl" }).click();
  const text = await page.evaluate(() => navigator.clipboard.readText());
  expect(text).toMatch(
    /^curl -g -X 'POST' 'http:\/\/127\.0\.0\.1:\d+\/payments\/done\?order=42' --compressed/,
  );
});

test("replay, allowed and refused", async ({ page, request }) => {
  const id = await api(request).hookId("payments/done");
  await page.goto(`/hooks/${id}/calls/c_cut`);
  await expect(page.getByRole("button", { name: "Replay" })).toBeDisabled();
  await expect(page.getByText(/not captured in full/)).toBeVisible();
  await page.goto(`/hooks/${id}/calls/c_json`);
  await page.getByRole("button", { name: "Replay" }).click();
  await expect(page.getByText(/^Replayed: /)).toBeVisible();
  await expect(page).not.toHaveURL(/c_json$/);
  await expect(page.getByText(/replay of/)).toBeVisible();
  await page.getByRole("tab", { name: "Diff" }).click();
  await expect(page.getByText(/the original \(c_json\)/)).toBeVisible();
});
