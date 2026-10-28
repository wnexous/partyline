import { MonitorIcon, MoonIcon, SunIcon } from "lucide-react";
import { type MessageKey, useT } from "@/shared/i18n";
import { usePrefsStore } from "@/shared/lib/prefs-store";
import type { ThemePreference } from "@/shared/lib/theme";
import { ChoiceGroup } from "@/shared/ui/molecules/choice-group";

const THEMES: { value: ThemePreference; icon: typeof SunIcon; label: MessageKey }[] = [
  { value: "system", icon: MonitorIcon, label: "theme.system" },
  { value: "light", icon: SunIcon, label: "theme.light" },
  { value: "dark", icon: MoonIcon, label: "theme.dark" },
];

/** The color scheme of this browser, as three cards (UI-23). */
export function ThemeChoice({ idBase }: { idBase: string }) {
  const t = useT();
  const theme = usePrefsStore((s) => s.theme);
  const setTheme = usePrefsStore((s) => s.setTheme);
  return (
    <ChoiceGroup
      legend={t("theme.label")}
      idBase={idBase}
      value={theme}
      onValueChange={(v) => setTheme(v as ThemePreference)}
      className="sm:grid-cols-3"
      emphasizeChecked
      options={THEMES.map(({ value, icon: Icon, label }) => ({
        value,
        icon: <Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />,
        label: t(label),
      }))}
    />
  );
}
