import { LanguagesIcon } from "lucide-react";
import { useUpdateMe } from "@/lib/api/queries";
import { useLiveStore } from "@/lib/live/store";
import { isLocale, LOCALES, type Locale, useT } from "@/shared/i18n";
import { Button } from "@/shared/ui/primitives/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/shared/ui/primitives/dropdown-menu";

/** Each language is named in itself, so it can be found from either locale. */
export const LOCALE_NAMES: Record<Locale, string> = {
  "pt-BR": "Português (Brasil)",
  en: "English",
};

/**
 * Switches the UI language for this browser (localStorage) and, inside the app, also stores it on
 * the account (PATCH /me) so other browsers start in it. The account write is best effort.
 */
export function useChooseLocale(syncAccount: boolean) {
  const setLocale = useLiveStore((s) => s.setLocale);
  const updateMe = useUpdateMe();
  return (value: string) => {
    if (!isLocale(value)) return;
    setLocale(value);
    if (syncAccount) updateMe.mutate({ locale: value });
  };
}

export function LanguageRadioGroup({ syncAccount = false }: { syncAccount?: boolean }) {
  const locale = useLiveStore((s) => s.locale);
  const choose = useChooseLocale(syncAccount);
  return (
    <DropdownMenuRadioGroup value={locale} onValueChange={choose}>
      {LOCALES.map((l) => (
        <DropdownMenuRadioItem key={l} value={l} lang={l}>
          {LOCALE_NAMES[l]}
        </DropdownMenuRadioItem>
      ))}
    </DropdownMenuRadioGroup>
  );
}

export function LanguageSwitch() {
  const t = useT();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label={t("locale.label")}>
          <LanguagesIcon aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-48">
        <DropdownMenuLabel>{t("locale.label")}</DropdownMenuLabel>
        <LanguageRadioGroup />
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
