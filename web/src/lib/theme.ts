/**
 * The theme choice, stored under themeKey. public/theme.js applies the
 * same rule before the first paint; keep the two in step.
 */
export type Theme = "light" | "dark" | "system";

export const themeKey = "tsrelay-theme";

const dark = () => window.matchMedia("(prefers-color-scheme: dark)");

export function storedTheme(): Theme {
  try {
    const stored = localStorage.getItem(themeKey);
    return stored === "light" || stored === "dark" ? stored : "system";
  } catch {
    return "system";
  }
}

export function applyTheme(theme: Theme): void {
  const on = theme === "dark" || (theme === "system" && dark().matches);
  document.documentElement.classList.toggle("dark", on);
}

export function saveTheme(theme: Theme): void {
  try {
    if (theme === "system") localStorage.removeItem(themeKey);
    else localStorage.setItem(themeKey, theme);
  } catch {
    // Blocked site data: the choice lasts until the page is reloaded.
  }
  applyTheme(theme);
}

/** watchSystemTheme follows the system's scheme until the returned function stops it. */
export function watchSystemTheme(): () => void {
  const media = dark();
  const onChange = () => applyTheme("system");
  media.addEventListener("change", onChange);
  return () => media.removeEventListener("change", onChange);
}
