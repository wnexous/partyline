import { Link, useNavigate } from "@tanstack/react-router";
import {
  ContactRoundIcon,
  FingerprintIcon,
  KeyRoundIcon,
  LanguagesIcon,
  LogOutIcon,
  type LucideIcon,
  MailPlusIcon,
  MessagesSquareIcon,
  MonitorSmartphoneIcon,
  PaletteIcon,
  PlugZapIcon,
  PlusIcon,
  ScrollTextIcon,
  SettingsIcon,
  UsersIcon,
} from "lucide-react";
import { Wordmark } from "@/components/brand";
import { ConversationList } from "@/components/conversation-list";
import { CreateRoomDialog } from "@/components/create-room-dialog";
import { IntelligenceAvatar } from "@/components/intelligence/intelligence-avatar";
import { LanguageRadioGroup } from "@/components/language-switch";
import { LiveIndicator } from "@/components/live-status";
import { SearchButton } from "@/components/search-button";
import { ThemeRadioGroup } from "@/components/theme-toggle";
import { Button } from "@/components/ui/button";
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
import { SidebarContent, SidebarFooter, SidebarHeader } from "@/components/ui/sidebar";
import { useLogout } from "@/lib/api/queries";
import { type Me, RoleAdmin } from "@/lib/api/types.gen";
import { type MessageKey, useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

interface NavItem {
  to:
    | "/"
    | "/agents"
    | "/connectors"
    | "/identities"
    | "/devices"
    | "/tokens"
    | "/settings"
    | "/admin/users"
    | "/admin/invites"
    | "/admin/audit";
  label: MessageKey;
  icon: LucideIcon;
  exact?: boolean;
}

interface NavGroup {
  label: MessageKey;
  items: NavItem[];
  adminOnly?: boolean;
}

/** Every destination outside a conversation: the account menu lists them, the palette finds them. */
export const NAV_GROUPS: NavGroup[] = [
  {
    label: "nav.groupWork",
    items: [{ to: "/", label: "nav.conversations", icon: MessagesSquareIcon, exact: true }],
  },
  {
    label: "nav.groupConnections",
    items: [
      // SPEC_DEVIATION (T44, LAY-06): Contacts opens the agents page until T47 adds /contacts.
      // Reason: the contacts page and its API arrive in later tasks; the entry must exist now.
      { to: "/agents", label: "nav.contacts", icon: ContactRoundIcon },
      { to: "/connectors", label: "nav.connectors", icon: PlugZapIcon },
      { to: "/identities", label: "nav.identities", icon: FingerprintIcon },
      { to: "/devices", label: "nav.devices", icon: MonitorSmartphoneIcon },
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
    items: [
      { to: "/admin/users", label: "nav.adminUsers", icon: UsersIcon },
      { to: "/admin/invites", label: "nav.adminInvites", icon: MailPlusIcon },
      { to: "/admin/audit", label: "nav.adminAudit", icon: ScrollTextIcon },
    ],
  },
];

/** The wide-screen column: the messenger panel beside the page, as tall as the window. */
export function AppSidebar({ me }: { me: Me }) {
  const t = useT();
  return (
    <aside
      aria-label={t("conversations.label")}
      className="sticky top-0 h-svh w-80 shrink-0 border-r border-sidebar-border"
    >
      <MessengerPanel me={me} />
    </aside>
  );
}

/**
 * Brand, account menu and search, the conversation list, and the live line. On a phone it is
 * the home screen itself (`screen`): the page heading, filling the window.
 */
export function MessengerPanel({ me, screen = false }: { me: Me; screen?: boolean }) {
  const t = useT();
  return (
    <div
      data-fill={screen ? "" : undefined}
      className={cn(
        "flex h-full min-h-0 flex-col bg-sidebar text-sidebar-foreground",
        screen && "flex-1",
      )}
    >
      <SidebarHeader className="gap-3 px-3 pt-4 pb-3">
        <div className="flex items-center justify-between gap-2">
          <Link
            to="/"
            className="rounded-md outline-none focus-visible:ring-2 focus-visible:ring-sidebar-ring"
          >
            <Wordmark />
          </Link>
          <AccountMenu me={me} />
        </div>
        <div className="flex gap-2">
          <SearchButton className="h-11 min-w-0 flex-1 md:h-9" />
          {/* The room dialog until the sidebar gets "New conversation" and "New group" (T45). */}
          <CreateRoomDialog
            trigger={
              <Button
                variant="outline"
                size="icon"
                className="size-11 shrink-0 border-sidebar-border bg-background/70 md:size-9"
                aria-label={t("rooms.new")}
              >
                <PlusIcon aria-hidden="true" />
              </Button>
            }
          />
        </div>
        {screen ? <h1 className="sr-only">{t("conversations.label")}</h1> : null}
      </SidebarHeader>

      <SidebarContent className="min-h-0 flex-1 gap-0 overscroll-contain border-t border-sidebar-border">
        <ConversationList />
      </SidebarContent>

      <SidebarFooter className="border-t border-sidebar-border px-4 py-2.5">
        <LiveIndicator className="text-sidebar-foreground/80" />
      </SidebarFooter>
    </div>
  );
}

function AccountMenu({ me }: { me: Me }) {
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
          className="grid size-11 shrink-0 place-items-center rounded-full outline-none focus-visible:ring-2 focus-visible:ring-sidebar-ring data-[state=open]:ring-2 data-[state=open]:ring-sidebar-ring/60"
        >
          <IntelligenceAvatar intelligence={me.intelligence} size="lg" decorative />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-60">
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
                  className="data-[status=active]:font-semibold"
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
            <LanguageRadioGroup syncAccount />
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
