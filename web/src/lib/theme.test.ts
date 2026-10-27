import { beforeEach, expect, test } from "vitest";
import { applyTheme, saveTheme, storedTheme, themeKey } from "./theme";

beforeEach(() => {
  localStorage.clear();
  document.documentElement.classList.remove("dark");
});

test("system is the default, and only light and dark are stored", () => {
  expect(storedTheme()).toBe("system");
  saveTheme("dark");
  expect(localStorage.getItem(themeKey)).toBe("dark");
  expect(document.documentElement.classList.contains("dark")).toBe(true);
  saveTheme("system");
  expect(localStorage.getItem(themeKey)).toBeNull();
  expect(document.documentElement.classList.contains("dark")).toBe(false);
  localStorage.setItem(themeKey, "purple");
  expect(storedTheme()).toBe("system");
});

test("system follows prefers-color-scheme", () => {
  const original = window.matchMedia;
  window.matchMedia = ((query: string) => ({
    ...original(query),
    matches: true,
  })) as typeof window.matchMedia;
  applyTheme("system");
  expect(document.documentElement.classList.contains("dark")).toBe(true);
  applyTheme("light");
  expect(document.documentElement.classList.contains("dark")).toBe(false);
  window.matchMedia = original;
});
