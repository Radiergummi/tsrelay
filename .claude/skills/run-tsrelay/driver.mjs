// Drives the web UI of a running dev relay (harness.sh start) with
// headless Chromium, one command per line on stdin:
//
//   node .claude/skills/run-tsrelay/driver.mjs <<'EOF'
//   nav /hooks
//   click role=link[name="busy"]
//   shot busy
//   EOF
//
// Commands:
//   user dev|admin|nobody|other   switch identity (port); takes effect on the next nav
//   theme light|dark              color scheme, applied at once
//   nav <path-or-url>             go to a path on the current user's port, wait for the network to settle
//   click <selector>              Playwright selector, e.g. text=Replay, role=tab[name="Diff"], css=…
//   fill <selector> <text>        type into an input (goes through React's onChange)
//   press <key>                   keyboard key, e.g. Enter, Escape
//   wait <selector>               wait until visible
//   text [selector]               print the inner text (default: main, else body)
//   shot <name>                   full-page screenshot to $TSRELAY_SHOTS/<name>.png
//   errors                        print console errors and failed requests so far
//
// Playwright comes from web/node_modules (pnpm install in web/).
import { createRequire } from "node:module";
import { mkdirSync } from "node:fs";
import path from "node:path";
import readline from "node:readline";

const root = path.resolve(import.meta.dirname, "../../..");
const { chromium } = createRequire(path.join(root, "web/package.json"))("@playwright/test");

const users = { dev: 8099, admin: 8098, nobody: 8097, other: 8096 };
const shots = process.env.TSRELAY_SHOTS ?? path.join(process.env.TMPDIR ?? "/tmp", "tsrelay-shots");
mkdirSync(shots, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });
let port = users.dev;
const errors = [];
page.on("console", (m) => m.type() === "error" && errors.push(`console: ${m.text()}`));
page.on("pageerror", (e) => errors.push(`pageerror: ${e.message}`));
page.on("requestfailed", (r) => errors.push(`failed: ${r.method()} ${r.url()} ${r.failure()?.errorText}`));
page.on("response", (r) => r.status() >= 500 && errors.push(`${r.status()}: ${r.url()}`));

const commands = {
  user: (name) => {
    if (!(name in users)) throw new Error(`unknown user ${name}`);
    port = users[name];
  },
  theme: (scheme) => page.emulateMedia({ colorScheme: scheme }),
  nav: async (to) => {
    await page.goto(new URL(to || "/", `http://127.0.0.1:${port}`).href, { waitUntil: "networkidle" });
    console.log(`at ${page.url()} — ${await page.title()}`);
  },
  click: (sel) => page.locator(sel).first().click(),
  fill: (rest) => {
    const [sel, ...text] = rest.split(" ");
    return page.locator(sel).first().fill(text.join(" "));
  },
  press: (key) => page.keyboard.press(key),
  wait: (sel) => page.locator(sel).first().waitFor({ state: "visible" }),
  text: async (sel) => {
    const target = sel || ((await page.locator("main").count()) ? "main" : "body");
    console.log(await page.locator(target).first().innerText());
  },
  shot: async (name) => {
    const file = path.join(shots, `${name || "shot"}.png`);
    // Finish CSS transitions at once, or a theme switch is caught halfway.
    await page.screenshot({ path: file, fullPage: true, animations: "disabled" });
    console.log(file);
  },
  errors: () => console.log(errors.length ? errors.join("\n") : "no errors"),
};

let failed = false;
for await (const line of readline.createInterface({ input: process.stdin })) {
  const trimmed = line.trim();
  if (!trimmed || trimmed.startsWith("#")) continue;
  const [cmd, ...rest] = trimmed.split(" ");
  const run = commands[cmd];
  try {
    if (!run) throw new Error(`unknown command ${cmd}`);
    await run(rest.join(" "));
  } catch (e) {
    console.error(`${trimmed}: ${e.message.split("\n")[0]}`);
    failed = true;
    break;
  }
}
await browser.close();
process.exit(failed ? 1 : 0);
