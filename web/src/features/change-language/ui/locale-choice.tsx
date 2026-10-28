import { LOCALES, useI18n } from "@/shared/i18n";
import { ChoiceGroup } from "@/shared/ui/molecules/choice-group";
import { LOCALE_NAMES, useChooseLocale } from "./language-switch";

/** The interface language, as cards; the choice is kept on the account too (PATCH /me). */
export function LocaleChoice({ idBase }: { idBase: string }) {
  const { t, locale } = useI18n();
  const chooseLocale = useChooseLocale(true);
  return (
    <ChoiceGroup
      legend={t("locale.label")}
      idBase={idBase}
      value={locale}
      onValueChange={chooseLocale}
      className="sm:grid-cols-2"
      emphasizeChecked
      options={LOCALES.map((l) => ({ value: l, label: LOCALE_NAMES[l], lang: l }))}
    />
  );
}
