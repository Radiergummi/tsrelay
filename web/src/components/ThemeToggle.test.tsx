import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test } from "vitest";
import { themeKey } from "@/lib/theme";
import { ThemeToggle } from "./ThemeToggle";

test("cycles light, dark and system, and remembers the choice", async () => {
  localStorage.clear();
  render(<ThemeToggle />);
  const button = screen.getByRole("button", { name: /theme: system/i });
  await userEvent.click(button);
  expect(localStorage.getItem(themeKey)).toBe("light");
  expect(button).toHaveAccessibleName(/theme: light/i);
  await userEvent.click(button);
  expect(localStorage.getItem(themeKey)).toBe("dark");
  expect(document.documentElement).toHaveClass("dark");
  await userEvent.click(button);
  expect(localStorage.getItem(themeKey)).toBeNull();
  expect(button).toHaveAccessibleName(/theme: system/i);
});
