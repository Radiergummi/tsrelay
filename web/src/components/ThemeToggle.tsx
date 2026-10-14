import { Monitor, Moon, Sun } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { saveTheme, storedTheme, watchSystemTheme, type Theme } from "@/lib/theme";

const next: Record<Theme, Theme> = { system: "light", light: "dark", dark: "system" };
const icons = { light: Sun, dark: Moon, system: Monitor };

/** ThemeToggle cycles light, dark and the system's choice. */
export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(storedTheme);
  const current = useRef(theme);
  useEffect(() => {
    current.current = theme;
  }, [theme]);
  useEffect(() => watchSystemTheme(() => current.current), []);
  const Icon = icons[theme];
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label={`Theme: ${theme}`}
      title={`Theme: ${theme}`}
      onClick={() => {
        const t = next[theme];
        saveTheme(t);
        setTheme(t);
      }}
    >
      <Icon />
    </Button>
  );
}
