/** Color scheme preference (UI-23). Keep THEME_KEY in sync with public/theme-init.js. */
export const THEME_KEY = "partyline.theme";
export const LOCALE_KEY = "partyline.locale";

export type ThemePreference = "system" | "light" | "dark";

export function readStorage(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function writeStorage(key: string, value: string | null) {
  try {
    if (value === null) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, value);
  } catch {
    // Storage unavailable (private mode, blocked): the preference lasts for this tab only.
  }
}

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

/** Applies the resolved scheme to <html> (the `dark` class drives the tokens). */
export function applyTheme(pref: ThemePreference) {
  const resolved = resolveTheme(pref);
  document.documentElement.classList.toggle("dark", resolved === "dark");
  document.documentElement.style.colorScheme = resolved;
}
