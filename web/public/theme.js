// Applies the stored theme before the first paint, so dark mode never
// flashes light. A separate file, because the CSP allows no inline script.
// Keep the rule in step with src/lib/theme.ts.
try {
  var stored = localStorage.getItem("tsrelay-theme");
  var dark =
    stored === "dark" || (stored !== "light" && matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", dark);
} catch {
  // Blocked site data: the default (light) stylesheet applies.
}
