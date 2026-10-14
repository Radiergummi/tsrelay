/**
 * The theme choice, stored under themeKey. public/theme.js applies the
 * same rule before the first paint; keep the two in step.
 */
export type Theme = "light" | "dark" | "system";

export const themeKey = "tsrelay-theme";

const dark = () => window.matchMedia("(prefers-color-scheme: dark)");

export function storedTheme(): Theme {
  try {
    const t = localStorage.getItem(themeKey);
    return t === "light" || t === "dark" ? t : "system";
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

/** watchSystemTheme follows the system's scheme while the choice is "system". */
export function watchSystemTheme(current: () => Theme): () => void {
  const media = dark();
  const onChange = () => {
    if (current() === "system") applyTheme("system");
  };
  media.addEventListener("change", onChange);
  return () => media.removeEventListener("change", onChange);
}
