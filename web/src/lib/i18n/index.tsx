import { createContext, type ReactNode, useCallback, useContext, useMemo } from "react";
import { type Dictionary, en, type MessageKey } from "./en";
import { ptBR } from "./pt-BR";

export type { MessageKey } from "./en";

export const LOCALES = ["pt-BR", "en"] as const;
export type Locale = (typeof LOCALES)[number];

export const dictionaries: Record<Locale, Dictionary> = { en, "pt-BR": ptBR };

export type TranslateVars = Record<string, string | number>;
export type Translate = (key: MessageKey, vars?: TranslateVars) => string;

export function isLocale(value: unknown): value is Locale {
  return typeof value === "string" && (LOCALES as readonly string[]).includes(value);
}

/** Picks pt-BR for any Portuguese browser language, English otherwise. */
export function detectLocale(languages: readonly string[]): Locale {
  return languages.some((l) => l.toLowerCase().startsWith("pt")) ? "pt-BR" : "en";
}

export function translate(locale: Locale, key: MessageKey, vars?: TranslateVars): string {
  const template = dictionaries[locale][key] ?? en[key] ?? key;
  if (!vars) return template;
  return template.replace(/\{(\w+)\}/g, (match, name: string) =>
    name in vars ? String(vars[name]) : match,
  );
}

/** Keys present in `reference` but missing (or empty) in `candidate`, and extra keys. */
export function dictionaryDiff(
  reference: Record<string, string>,
  candidate: Record<string, string>,
): { missing: string[]; extra: string[] } {
  const missing = Object.keys(reference).filter((k) => !candidate[k]?.trim());
  const extra = Object.keys(candidate).filter((k) => !(k in reference));
  return { missing, extra };
}

interface I18nValue {
  locale: Locale;
  t: Translate;
}

const I18nContext = createContext<I18nValue | null>(null);

export function I18nProvider({ locale, children }: { locale: Locale; children: ReactNode }) {
  const t = useCallback<Translate>((key, vars) => translate(locale, key, vars), [locale]);
  const value = useMemo(() => ({ locale, t }), [locale, t]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

/** Returns the translate function for the current locale (English outside a provider). */
export function useT(): Translate {
  return useI18n().t;
}

export function useI18n(): I18nValue {
  const ctx = useContext(I18nContext);
  if (ctx) return ctx;
  return { locale: "en", t: (key, vars) => translate("en", key, vars) };
}
