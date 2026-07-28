import { useCallback, useEffect, useState } from "react";

export type Theme = "light" | "dark";

function getInitialTheme(): Theme {
  const stored = localStorage.getItem("theme");
  if (stored === "light" || stored === "dark") return stored;
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

/**
 * Drives the `data-theme` attribute on <html>. index.html ships
 * data-theme="dark" so the first paint is already correct — no flash.
 */
export function useTheme() {
  const [theme, setTheme] = useState<Theme>(getInitialTheme);

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme);
    localStorage.setItem("theme", theme);
  }, [theme]);

  const toggleTheme = useCallback(
    () => setTheme((p) => (p === "dark" ? "light" : "dark")),
    [],
  );

  return { theme, toggleTheme };
}
