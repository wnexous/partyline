import { MonitorIcon, MoonIcon, SunIcon } from "lucide-react";
import { useT } from "@/shared/i18n";
import { usePrefsStore } from "@/shared/lib/prefs-store";
import type { ThemePreference } from "@/shared/lib/theme";
import {
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
} from "@/shared/ui/primitives/dropdown-menu";

const OPTIONS = [
  { value: "system", icon: MonitorIcon, label: "theme.system" },
  { value: "light", icon: SunIcon, label: "theme.light" },
  { value: "dark", icon: MoonIcon, label: "theme.dark" },
] as const;

/** Radio items for the color scheme; used in the account menu and the theme button. */
export function ThemeRadioGroup() {
  const t = useT();
  const theme = usePrefsStore((s) => s.theme);
  const setTheme = usePrefsStore((s) => s.setTheme);
  return (
    <DropdownMenuRadioGroup value={theme} onValueChange={(v) => setTheme(v as ThemePreference)}>
      {OPTIONS.map(({ value, icon: Icon, label }) => (
        <DropdownMenuRadioItem key={value} value={value}>
          <Icon aria-hidden="true" />
          {t(label)}
        </DropdownMenuRadioItem>
      ))}
    </DropdownMenuRadioGroup>
  );
}
