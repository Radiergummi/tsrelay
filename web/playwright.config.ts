import { defineConfig, devices } from "@playwright/test";

// The tests share one relay (the Go dev harness) and change its state, so
// they run one at a time. Every flow runs in light and in dark mode.
export default defineConfig({
  testDir: "e2e",
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["github"], ["html", { open: "never" }]] : "list",
  use: { baseURL: "http://127.0.0.1:8099", trace: "retain-on-failure" },
  projects: [
    { name: "light", use: { ...devices["Desktop Chrome"], colorScheme: "light" } },
    { name: "dark", use: { ...devices["Desktop Chrome"], colorScheme: "dark" } },
  ],
  webServer: {
    command: "go test ./internal/server -run '^TestUIDev$' -count=1 -timeout 0",
    cwd: "..",
    env: { TSRELAY_UI_DEV: "1" },
    url: "http://127.0.0.1:8099/",
    reuseExistingServer: !process.env.CI,
    timeout: 180_000,
  },
});
