import { Monitor, Moon, Sun } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { saveTheme, storedTheme, watchSystemTheme, type Theme } from "@/lib/theme";

const next: Record<Theme, Theme> = { system: "light", light: "dark", dark: "system" };
const icons = { light: Sun, dark: Moon, system: Monitor };

/** ThemeToggle cycles light, dark and the system's choice. */
export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(storedTheme);
  useEffect(() => (theme === "system" ? watchSystemTheme() : undefined), [theme]);
  const Icon = icons[theme];
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label={`Theme: ${theme}`}
      title={`Theme: ${theme}`}
      onClick={() => {
        const nextTheme = next[theme];
        saveTheme(nextTheme);
        setTheme(nextTheme);
      }}
    >
      <Icon />
    </Button>
  );
}
