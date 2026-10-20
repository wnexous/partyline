import { Link, useNavigate } from "@tanstack/react-router";
import {
  BotIcon,
  ChevronsUpDownIcon,
  FingerprintIcon,
  KeyRoundIcon,
  LanguagesIcon,
  LayoutDashboardIcon,
  LogOutIcon,
  type LucideIcon,
  MessagesSquareIcon,
  PaletteIcon,
  PlugZapIcon,
  SettingsIcon,
  UsersIcon,
} from "lucide-react";
import { Wordmark } from "@/components/brand";
import { LanguageRadioGroup } from "@/components/language-switch";
import { LiveIndicator } from "@/components/live-status";
import { ThemeRadioGroup } from "@/components/theme-toggle";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from "@/components/ui/sidebar";
import { useLogout } from "@/lib/api/queries";
import { type Me, RoleAdmin } from "@/lib/api/types.gen";
import { type MessageKey, useT } from "@/lib/i18n";

interface NavItem {
  to:
    | "/"
    | "/rooms"
    | "/agents"
    | "/connectors"
    | "/identities"
    | "/tokens"
    | "/settings"
    | "/admin/users";
  label: MessageKey;
  icon: LucideIcon;
  exact?: boolean;
}

interface NavGroup {
  label: MessageKey;
  items: NavItem[];
  adminOnly?: boolean;
}

export const NAV_GROUPS: NavGroup[] = [
  {
    label: "nav.groupWork",
    items: [
      { to: "/", label: "nav.overview", icon: LayoutDashboardIcon, exact: true },
      { to: "/rooms", label: "nav.rooms", icon: MessagesSquareIcon },
    ],
  },
  {
    label: "nav.groupConnections",
    items: [
      { to: "/agents", label: "nav.agents", icon: BotIcon },
      { to: "/connectors", label: "nav.connectors", icon: PlugZapIcon },
      { to: "/identities", label: "nav.identities", icon: FingerprintIcon },
    ],
  },
  {
    label: "nav.groupAccount",
    items: [
      { to: "/tokens", label: "nav.tokens", icon: KeyRoundIcon },
      { to: "/settings", label: "nav.settings", icon: SettingsIcon },
    ],
  },
  {
    label: "nav.admin",
    adminOnly: true,
    items: [{ to: "/admin/users", label: "nav.adminUsers", icon: UsersIcon }],
  },
];

const ACTIVE =
  "data-[active=true]:bg-tape data-[active=true]:font-semibold data-[active=true]:text-tape-foreground dark:data-[active=true]:bg-tape/14 dark:data-[active=true]:text-tape";

function initials(me: Me): string {
  const source = me.display_name.trim() || me.username;
  const parts = source.split(/\s+/).filter(Boolean);
  const letters = parts.length > 1 ? `${parts[0]?.[0]}${parts.at(-1)?.[0]}` : source.slice(0, 2);
  return letters.toUpperCase();
}

export function AppSidebar({ me }: { me: Me }) {
  const t = useT();
  const { isMobile, setOpenMobile } = useSidebar();
  const isAdmin = me.role === RoleAdmin;
  const closeOnMobile = () => {
    if (isMobile) setOpenMobile(false);
  };

  return (
    <Sidebar collapsible="offcanvas" aria-label={t("nav.label")}>
      <SidebarHeader className="px-4 pt-5 pb-3">
        <Link
          to="/"
          onClick={closeOnMobile}
          className="rounded-md outline-none focus-visible:ring-2 focus-visible:ring-sidebar-ring"
        >
          <Wordmark />
        </Link>
      </SidebarHeader>

      <SidebarContent className="gap-0 px-1">
        {NAV_GROUPS.filter((g) => !g.adminOnly || isAdmin).map((group) => (
          <SidebarGroup key={group.label}>
            <SidebarGroupLabel className="label-caps text-[0.68rem] text-sidebar-foreground/60">
              {t(group.label)}
            </SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {group.items.map((item) => (
                  <SidebarMenuItem key={item.to}>
                    <SidebarMenuButton asChild className={ACTIVE}>
                      <Link
                        to={item.to}
                        activeOptions={{ exact: item.exact ?? false }}
                        activeProps={{ "data-active": true }}
                        onClick={closeOnMobile}
                      >
                        <item.icon aria-hidden="true" />
                        <span>{t(item.label)}</span>
                      </Link>
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                ))}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        ))}
      </SidebarContent>

      <SidebarFooter className="gap-3 border-t border-sidebar-border px-3 py-3">
        <LiveIndicator className="px-2 text-sidebar-foreground/80" />
        <UserMenu me={me} initials={initials(me)} />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  );
}

function UserMenu({ me, initials }: { me: Me; initials: string }) {
  const t = useT();
  const navigate = useNavigate();
  const logout = useLogout();

  const onLogout = () => {
    logout.mutate(undefined, {
      onSettled: () => navigate({ to: "/login", search: { redirect: undefined } }),
    });
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <SidebarMenuButton
          size="lg"
          aria-label={t("shell.userMenu")}
          className="data-[state=open]:bg-sidebar-accent"
        >
          <span
            aria-hidden="true"
            className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary text-xs font-semibold text-primary-foreground [font-stretch:90%]"
          >
            {initials}
          </span>
          <span className="grid min-w-0 flex-1 text-left leading-tight">
            <span className="truncate text-sm font-medium">{me.display_name || me.username}</span>
            <span className="truncate font-mono text-xs text-sidebar-foreground/65">
              @{me.username}
              {me.role === RoleAdmin ? ` · ${t("shell.roleAdmin")}` : ""}
            </span>
          </span>
          <ChevronsUpDownIcon className="ml-auto size-4 opacity-60" aria-hidden="true" />
        </SidebarMenuButton>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        side="top"
        align="start"
        className="w-(--radix-dropdown-menu-trigger-width) min-w-56"
      >
        <DropdownMenuLabel className="font-normal text-muted-foreground">
          @{me.username}
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuSub>
          <DropdownMenuSubTrigger>
            <PaletteIcon aria-hidden="true" />
            {t("theme.label")}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent>
            <ThemeRadioGroup />
          </DropdownMenuSubContent>
        </DropdownMenuSub>
        <DropdownMenuSub>
          <DropdownMenuSubTrigger>
            <LanguagesIcon aria-hidden="true" />
            {t("locale.label")}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent>
            <LanguageRadioGroup />
          </DropdownMenuSubContent>
        </DropdownMenuSub>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onLogout} disabled={logout.isPending}>
          <LogOutIcon aria-hidden="true" />
          {t("shell.logout")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
