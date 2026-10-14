import { api, expect, test } from "./fixtures";

test("calls arrive live at the top of the list", async ({ page, request }) => {
  const id = await api(request).hookId("payments/done");
  await page.goto(`/hooks/${id}`);
  await expect(page.getByRole("heading", { level: 1, name: "payments/done" })).toBeVisible();
  await api(request).sendTest(id, `/arrived-${test.info().project.name}`);
  await expect(page.getByRole("region", { name: "Calls" }).getByRole("link").first()).toContainText(
    `arrived-${test.info().project.name}`,
  );
});

test("filters and keyboard navigation", async ({ page, request }) => {
  const id = await api(request).hookId("busy");
  await page.goto(`/hooks/${id}`);
  const calls = page.getByRole("region", { name: "Calls" }).getByRole("link");
  await expect(calls).toHaveCount(100);
  await page.getByRole("button", { name: /^5xx/ }).click();
  await expect(page).toHaveURL(/status=5xx/);
  // The filter is applied client-side on the next render, after the URL
  // changes; wait for the list to actually narrow before counting it.
  await expect(calls).not.toHaveCount(100);
  const fives = await calls.count();
  expect(fives).toBeGreaterThan(0);
  expect(fives).toBeLessThan(100);
  await page.keyboard.press("/");
  // Each keystroke writes the path filter to the URL via setSearchParams,
  // which (per react-router's own docs) does not build on a prior call
  // still in flight within the same tick; a human-paced delay keeps
  // synthetic typing from outrunning that round trip.
  await page.keyboard.type("item/9", { delay: 60 });
  await expect(page).toHaveURL(/path=item%2F9/);
  await page.locator("body").click({ position: { x: 1, y: 1 } });
  await page.keyboard.press("j");
  await expect(page).toHaveURL(new RegExp(`/hooks/${id}/calls/c_busy_\\d+\\?`));
  // The URL updates (and this assertion can pass) slightly ahead of the
  // component re-render that catches the shortcuts handler up with the
  // new call; wait for that render's own signal before the next shortcut.
  await expect(page.locator('[aria-current="true"]')).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page).toHaveURL(new RegExp(`/hooks/${id}\\?`));
  await page.keyboard.press("?");
  await expect(page.getByRole("dialog", { name: "Keyboard shortcuts" })).toBeVisible();
});

test("narrow screens stack the panes", async ({ page, request }) => {
  await page.setViewportSize({ width: 700, height: 900 });
  const id = await api(request).hookId("payments/done");
  await page.goto(`/hooks/${id}/calls/c_json`);
  await expect(page.getByRole("region", { name: "Calls" })).toBeHidden();
  await page.getByRole("link", { name: "← Back to calls" }).click();
  await expect(page.getByRole("region", { name: "Calls" })).toBeVisible();
});
