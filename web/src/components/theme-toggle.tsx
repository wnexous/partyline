import { MonitorIcon, MoonIcon, SunIcon } from "lucide-react";
import { useLiveStore } from "@/lib/live/store";
import type { ThemePreference } from "@/lib/theme";
import { useT } from "@/shared/i18n";
import { Button } from "@/shared/ui/primitives/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/shared/ui/primitives/dropdown-menu";

const OPTIONS = [
  { value: "system", icon: MonitorIcon, label: "theme.system" },
  { value: "light", icon: SunIcon, label: "theme.light" },
  { value: "dark", icon: MoonIcon, label: "theme.dark" },
] as const;

/** Radio items for the color scheme; used in the account menu and the theme button. */
export function ThemeRadioGroup() {
  const t = useT();
  const theme = useLiveStore((s) => s.theme);
  const setTheme = useLiveStore((s) => s.setTheme);
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

export function ThemeToggle() {
  const t = useT();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label={t("theme.label")}>
          <SunIcon className="dark:hidden" aria-hidden="true" />
          <MoonIcon className="hidden dark:block" aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-40">
        <DropdownMenuLabel>{t("theme.label")}</DropdownMenuLabel>
        <ThemeRadioGroup />
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
