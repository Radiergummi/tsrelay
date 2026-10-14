import { test as base, expect, type APIRequestContext } from "@playwright/test";

export { expect };

/** users are the dev harness's identities, one per port (internal/server/uidev_test.go). */
export const users = {
  dev: "http://127.0.0.1:8099",
  admin: "http://127.0.0.1:8098",
  nobody: "http://127.0.0.1:8097",
  other: "http://127.0.0.1:8096",
} as const;

export const CSP =
  "default-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'";

type CSPWindow = { __csp?: string[] };

/**
 * test fails any test during which the page reported a CSP violation,
 * through the securitypolicyviolation event or the console, or opened a
 * JavaScript dialog (a hostile capture that ran would).
 */
export const test = base.extend<{ cspGuard: void }>({
  cspGuard: [
    async ({ page }, use) => {
      const problems: string[] = [];
      await page.addInitScript(() => {
        document.addEventListener("securitypolicyviolation", (e) => {
          const w = window as unknown as CSPWindow;
          (w.__csp ??= []).push(`${e.violatedDirective} ${e.blockedURI}`);
        });
      });
      page.on("console", (m) => {
        if (/content security policy/i.test(m.text())) problems.push(m.text());
      });
      page.on("dialog", (d) => {
        problems.push(`dialog: ${d.message()}`);
        void d.dismiss();
      });
      await use();
      const reported = await page
        .evaluate(() => (window as unknown as CSPWindow).__csp ?? [])
        .catch(() => []);
      expect([...problems, ...reported], "CSP violations or dialogs").toEqual([]);
    },
    { auto: true },
  ],
});

/** api talks to the relay as one of the harness's users. */
export function api(request: APIRequestContext, user: keyof typeof users = "dev") {
  const base = users[user];
  return {
    async hookId(path: string): Promise<string> {
      const list = (await (await request.get(`${base}/api/v1/hooks?all=true`)).json()) as {
        id: string;
        path: string;
      }[];
      const mine = list.length
        ? list
        : ((await (await request.get(`${base}/api/v1/hooks`)).json()) as {
            id: string;
            path: string;
          }[]);
      const h = mine.find((x) => x.path === path);
      if (!h) throw new Error(`no hook ${path}`);
      return h.id;
    },
    async createHook(path: string): Promise<string> {
      const r = await request.post(`${base}/api/v1/hooks`, { data: { path } });
      expect(r.status()).toBe(201);
      return ((await r.json()) as { id: string }).id;
    },
    async deleteHook(id: string): Promise<void> {
      await request.delete(`${base}/api/v1/hooks/${id}`);
    },
    async sendTest(hookId: string, path: string): Promise<string> {
      const r = await request.post(`${base}/api/v1/hooks/${hookId}/test`, {
        data: {
          method: "POST",
          path,
          headers: { "Content-Type": ["application/json"] },
          body: '{"e2e":true}',
        },
      });
      expect(r.status()).toBe(201);
      return ((await r.json()) as { id: string }).id;
    },
  };
}
