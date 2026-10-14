import { api, expect, test } from "./fixtures";

type Vitals = { __lcp: number; __cls: number; __inp: number };

test("the hook page with 100 calls meets the Web Vitals targets", async ({ page, request }) => {
  await page.addInitScript(() => {
    const w = window as unknown as Vitals;
    w.__lcp = 0;
    w.__cls = 0;
    w.__inp = 0;
    new PerformanceObserver((l) => {
      for (const e of l.getEntries()) w.__lcp = e.startTime;
    }).observe({ type: "largest-contentful-paint", buffered: true });
    new PerformanceObserver((l) => {
      for (const e of l.getEntries() as unknown as { value: number; hadRecentInput: boolean }[])
        if (!e.hadRecentInput) w.__cls += e.value;
    }).observe({ type: "layout-shift", buffered: true });
    new PerformanceObserver((l) => {
      for (const e of l.getEntries()) w.__inp = Math.max(w.__inp, e.duration);
    }).observe({ type: "event", buffered: true, durationThreshold: 16 } as PerformanceObserverInit);
  });
  const id = await api(request).hookId("busy");
  await page.goto(`/hooks/${id}`);
  const calls = page.getByRole("region", { name: "Calls" }).getByRole("link");
  await expect(calls).toHaveCount(100);
  for (let i = 0; i < 5; i++) await page.keyboard.press("j");
  await calls.nth(40).click();
  await page.getByRole("tab", { name: "Response" }).click();
  await page.waitForTimeout(500);
  const v = await page.evaluate(() => {
    const w = window as unknown as Vitals;
    return { lcp: w.__lcp, cls: w.__cls, inp: w.__inp };
  });
  test.info().annotations.push({ type: "vitals", description: JSON.stringify(v) });
  expect(v.lcp, "LCP (ms)").toBeLessThan(1500);
  expect(v.cls, "CLS").toBeLessThan(0.05);
  expect(v.inp, "INP (ms)").toBeLessThan(200);
});
