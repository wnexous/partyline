/** Preference keys in localStorage; the theme key is also set by public/theme-init.js. */
export const THEME_KEY = "partyline.theme";
export const LOCALE_KEY = "partyline.locale";

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
