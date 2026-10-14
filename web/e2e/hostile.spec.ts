import { api, expect, test } from "./fixtures";

// The cspGuard fixture fails the test if any of these opened a dialog or
// tripped the CSP; these checks add that nothing became an element.
for (const call of ["c_hostile_html", "c_hostile_json", "c_hostile_form"]) {
  test(`${call} renders as text`, async ({ page, request }) => {
    const id = await api(request).hookId("hostile");
    await page.goto(`/hooks/${id}/calls/${call}`);
    const inspector = page.getByRole("region", { name: "Inspector" });
    await expect(inspector.getByRole("heading", { level: 2 })).toBeVisible();
    for (const view of ["Raw", "Tree"]) {
      const toggle = inspector.getByRole("button", { name: view, exact: true });
      if (await toggle.count()) await toggle.click();
      await expect(
        inspector.locator("script, img, iframe, object, embed, form, style"),
      ).toHaveCount(0);
    }
    const links = await inspector
      .locator("a")
      .evaluateAll((as) => as.map((a) => a.getAttribute("href") ?? ""));
    expect(links.every((h) => h.startsWith("/hooks/"))).toBe(true);
  });
}

test("hostile paths, headers and bidi controls are shown literally", async ({ page, request }) => {
  const id = await api(request).hookId("hostile");
  await page.goto(`/hooks/${id}/calls/c_hostile_html`);
  const inspector = page.getByRole("region", { name: "Inspector" });
  await expect(inspector).toContainText(`<script>alert(4)</script>`);
  await expect(inspector).toContainText(`</td><script>alert(6)</script>`);
  await expect(inspector).toContainText("⟨U+202E⟩");
});
