import { readStorage, THEME_KEY } from "@/shared/lib/storage";

/** Browser chrome colour per scheme: the `--background` token in sRGB. */
export const THEME_COLORS = { light: "#fafafa", dark: "#0f0f0f" } as const;

export type ThemePreference = "system" | "light" | "dark";

export function readThemePreference(): ThemePreference {
  const stored = readStorage(THEME_KEY);
  return stored === "light" || stored === "dark" ? stored : "system";
}

export function systemPrefersDark(): boolean {
  return typeof window.matchMedia === "function"
    ? window.matchMedia("(prefers-color-scheme: dark)").matches
    : false;
}

export function resolveTheme(pref: ThemePreference): "light" | "dark" {
  if (pref === "system") return systemPrefersDark() ? "dark" : "light";
  return pref;
}

/**
 * Applies the resolved scheme to <html> (the `dark` class drives the tokens) and points every
 * theme-color meta at it, so an explicit choice also wins over the system scheme in the chrome.
 */
export function applyTheme(pref: ThemePreference) {
  const resolved = resolveTheme(pref);
  document.documentElement.classList.toggle("dark", resolved === "dark");
  document.documentElement.style.colorScheme = resolved;
  for (const meta of document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')) {
    meta.content = THEME_COLORS[resolved];
  }
}
