import { Link, useNavigate } from "@tanstack/react-router";
import { LanguagesIcon, LogOutIcon, PaletteIcon } from "lucide-react";
import { IntelligenceAvatar } from "@/components/intelligence/intelligence-avatar";
import { LanguageRadioGroup } from "@/components/language-switch";
import { ThemeRadioGroup } from "@/components/theme-toggle";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useLogout } from "@/lib/api/queries";
import { type Me, RoleAdmin } from "@/lib/api/types.gen";
import { useT } from "@/lib/i18n";
import { NAV_GROUPS } from "@/lib/nav";
import { cn } from "@/lib/utils";

/** A row's height: a finger on touch, a pointer's row on a wide screen (SET-04). */
const ROW = "min-h-11 md:min-h-9";

/**
 * The avatar's menu (SET-03): the pages that are the person's own under "Account", the ways
 * elsewhere under "Go to" and, for an instance admin, the instance's pages under "Admin"; then
 * the theme and the language it already owned, and Log out. Every row is a finger's height and
 * the menu stays inside a 320 px screen at a 125% root font.
 */
export function AccountMenu({ me }: { me: Me }) {
  const t = useT();
  const navigate = useNavigate();
  const logout = useLogout();
  const isAdmin = me.role === RoleAdmin;
  const { display_name: name, handle } = me.intelligence;

  const onLogout = () => {
    logout.mutate(undefined, {
      onSettled: () => navigate({ to: "/login", search: { redirect: undefined } }),
    });
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={t("shell.userMenu")}
          className="grid size-11 shrink-0 place-items-center rounded-full outline-none focus-visible:ring-2 focus-visible:ring-sidebar-ring data-[state=open]:ring-2 data-[state=open]:ring-sidebar-ring/70"
        >
          <IntelligenceAvatar intelligence={me.intelligence} size="lg" decorative />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-60 max-w-[calc(100vw-1rem)]">
        <DropdownMenuLabel className="font-normal">
          <span className="block truncate font-semibold text-foreground">{name}</span>
          <span className="block truncate font-mono text-xs text-muted-foreground">
            @{handle}
            {isAdmin ? ` · ${t("shell.roleAdmin")}` : ""}
          </span>
        </DropdownMenuLabel>
        {NAV_GROUPS.filter((g) => !g.adminOnly || isAdmin).map((group) => (
          <DropdownMenuGroup key={group.label}>
            <DropdownMenuSeparator />
            <DropdownMenuLabel className="label-caps py-1 text-[0.68rem] text-muted-foreground">
              {t(group.label)}
            </DropdownMenuLabel>
            {group.items.map((item) => (
              <DropdownMenuItem key={item.to} asChild>
                <Link
                  to={item.to}
                  activeOptions={{ exact: item.exact ?? false }}
                  className={cn(ROW, "data-[status=active]:font-semibold")}
                >
                  <item.icon aria-hidden="true" />
                  {t(item.label)}
                </Link>
              </DropdownMenuItem>
            ))}
          </DropdownMenuGroup>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuSub>
          <DropdownMenuSubTrigger className={ROW}>
            <PaletteIcon aria-hidden="true" />
            {t("theme.label")}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent>
            <ThemeRadioGroup />
          </DropdownMenuSubContent>
        </DropdownMenuSub>
        <DropdownMenuSub>
          <DropdownMenuSubTrigger className={ROW}>
            <LanguagesIcon aria-hidden="true" />
            {t("locale.label")}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent>
            <LanguageRadioGroup syncAccount />
          </DropdownMenuSubContent>
        </DropdownMenuSub>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onLogout} disabled={logout.isPending} className={ROW}>
          <LogOutIcon aria-hidden="true" />
          {t("shell.logout")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
