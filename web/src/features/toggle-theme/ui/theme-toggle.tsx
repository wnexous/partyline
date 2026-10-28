import { MoonIcon, SunIcon } from "lucide-react";
import { useT } from "@/shared/i18n";
import { ThemeRadioGroup } from "@/shared/ui/molecules/theme-radio-group";
import { Button } from "@/shared/ui/primitives/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/shared/ui/primitives/dropdown-menu";

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
