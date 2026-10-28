import { create } from "zustand";
import { detectLocale, isLocale, type Locale } from "@/shared/i18n";
import { LOCALE_KEY, readStorage, THEME_KEY, writeStorage } from "@/shared/lib/storage";
import { readThemePreference, type ThemePreference } from "@/shared/lib/theme";

/*
 * Per-browser preferences of the app shell: colour theme, language and the command palette. They
 * are written to storage as they change and are the same in every layer.
 */

export interface PrefsState {
  theme: ThemePreference;
  locale: Locale;
  paletteOpen: boolean;
  setTheme: (theme: ThemePreference) => void;
  setLocale: (locale: Locale) => void;
  setPaletteOpen: (open: boolean) => void;
}

function initialLocale(): Locale {
  const stored = readStorage(LOCALE_KEY);
  if (isLocale(stored)) return stored;
  return detectLocale(typeof navigator === "undefined" ? [] : (navigator.languages ?? []));
}

export const usePrefsStore = create<PrefsState>()((set) => ({
  theme: readThemePreference(),
  locale: initialLocale(),
  paletteOpen: false,

  setTheme: (theme) => {
    writeStorage(THEME_KEY, theme === "system" ? null : theme);
    set({ theme });
  },
  setLocale: (locale) => {
    writeStorage(LOCALE_KEY, locale);
    set({ locale });
  },
  setPaletteOpen: (paletteOpen) => set({ paletteOpen }),
}));
