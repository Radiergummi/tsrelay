import { api, expect, test, users } from "./fixtures";

test("an admin without the receiver role sees every hook as metadata only", async ({
  page,
  request,
}) => {
  await page.goto(`${users.admin}/`);
  await expect(page).toHaveURL(`${users.admin}/admin`);
  await expect(page.getByText("other@example.com")).toBeVisible();
  const theirs = await api(request, "admin").hookId("theirs");
  const detail = page.waitForResponse((r) => r.url().endsWith(`/calls/c_theirs`));
  await page.goto(`${users.admin}/hooks/${theirs}/calls/c_theirs`);
  const body = await (await detail).text();
  expect(body).not.toContain("s3cr3t");
  expect(body).not.toContain(`"body"`);
  expect(body).not.toContain(`"replay"`);
  await expect(
    page.getByText(/only the hook's owner sees its query, headers and bodies/),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Replay" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Send test request" })).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText("s3cr3t");
});

test("an admin can delete another user's hook", async ({ page, request }) => {
  const id = await api(request, "other").createHook(
    `doomed-${test.info().project.name}-${Date.now()}`,
  );
  await page.goto(`${users.admin}/admin`);
  const row = page.getByRole("row").filter({ has: page.locator(`a[href="/hooks/${id}"]`) });
  await row.getByRole("button", { name: /^Delete / }).click();
  await page.getByRole("button", { name: "Delete hook" }).click();
  await expect(row).toHaveCount(0);
});
