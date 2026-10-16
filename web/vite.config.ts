import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";
import { precompress } from "./plugins/precompress.ts";

export default defineConfig({
  plugins: [react(), tailwindcss(), precompress()],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "src") } },
  build: {
    // The budget script reads .vite/manifest.json. The Go handler never
    // serves hidden files, so it stays private.
    manifest: true,
    modulePreload: { polyfill: false },
    target: "es2022",
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    // The dev harness (make dev) answers the resources and the CLI's
    // inspect links. Resources share their URLs with the app's pages, so a
    // page load (Accept: text/html) gets Vite's index.html instead. Vite
    // matches the URL with its query, as in /hooks?all=true.
    proxy: {
      "^/((hooks|channels)(/[^?]*)?|openapi(\\.[^?]*)?)?(\\?.*)?$": {
        target: "http://127.0.0.1:8099",
        bypass: (req) => (req.headers.accept?.includes("text/html") ? "/index.html" : undefined),
      },
      "/inspect": "http://127.0.0.1:8099",
    },
  },
  test: {
    environment: "jsdom",
    environmentOptions: { jsdom: { url: "http://127.0.0.1:5173/" } },
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}", "plugins/**/*.test.ts"],
    css: false,
  },
});
