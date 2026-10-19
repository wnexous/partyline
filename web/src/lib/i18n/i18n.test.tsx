import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { en } from "./en";
import {
  detectLocale,
  dictionaries,
  dictionaryDiff,
  I18nProvider,
  LOCALES,
  translate,
  useT,
} from "./index";
import { ptBR } from "./pt-BR";

const placeholders = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();

describe("i18n dictionaries (UI-25)", () => {
  it.each(LOCALES)("%s has exactly the English keys, none empty", (locale) => {
    expect(dictionaryDiff(en, dictionaries[locale])).toEqual({ missing: [], extra: [] });
  });

  it("pt-BR keeps every placeholder of the English string", () => {
    for (const key of Object.keys(en) as (keyof typeof en)[]) {
      expect(placeholders(ptBR[key]), key).toEqual(placeholders(en[key]));
    }
  });

  it("the parity check fails when a pt-BR key is missing or empty", () => {
    const { "common.save": _removed, ...withoutSave } = ptBR;
    expect(dictionaryDiff(en, withoutSave).missing).toEqual(["common.save"]);
    expect(dictionaryDiff(en, { ...ptBR, "common.cancel": " " }).missing).toEqual([
      "common.cancel",
    ]);
    expect(dictionaryDiff(en, { ...ptBR, "stray.key": "x" }).extra).toEqual(["stray.key"]);
  });
});

describe("translate", () => {
  it("interpolates placeholders in the chosen locale", () => {
    expect(translate("pt-BR", "validation.passwordMin", { min: 10 })).toBe(
      "Pelo menos 10 caracteres",
    );
    expect(translate("en", "validation.passwordMin", { min: 10 })).toBe("At least 10 characters");
  });

  it("detects pt-BR for Portuguese browsers and English otherwise", () => {
    expect(detectLocale(["pt-BR", "en-US"])).toBe("pt-BR");
    expect(detectLocale(["pt"])).toBe("pt-BR");
    expect(detectLocale(["en-US", "pt-BR"])).toBe("pt-BR");
    expect(detectLocale(["de-DE", "en"])).toBe("en");
    expect(detectLocale([])).toBe("en");
  });

  it("useT follows the provider locale", () => {
    function Label() {
      return <span>{useT()("common.save")}</span>;
    }
    render(
      <I18nProvider locale="pt-BR">
        <Label />
      </I18nProvider>,
    );
    expect(screen.getByText("Salvar")).toBeInTheDocument();
  });
});
