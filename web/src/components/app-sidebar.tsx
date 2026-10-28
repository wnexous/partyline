import { Link } from "@tanstack/react-router";
import { MessageSquarePlusIcon, UsersRoundIcon } from "lucide-react";
import { useState } from "react";
import { AccountMenu } from "@/components/account-menu";
import { Wordmark } from "@/shared/ui/atoms/brand";
import { ConversationList } from "@/components/conversation-list";
import { ConversationSearch } from "@/components/conversation-search";
import { LiveIndicator } from "@/shared/ui/molecules/live-status";
import { NewConversationDialog } from "@/components/new-conversation-dialog";
import { NewGroupDialog } from "@/components/new-group-dialog";
import { OrgSwitcher } from "@/components/org-switcher";
import { useLiveStore } from "@/lib/live/store";
import { OrganizationMenu } from "@/components/organization-menu";
import { SearchButton } from "@/shared/ui/molecules/search-button";
import { Button } from "@/shared/ui/primitives/button";
import { SidebarContent, SidebarFooter, SidebarHeader } from "@/shared/ui/primitives/sidebar";
import { useCurrentOrg } from "@/hooks/use-current-org";
import type { Me } from "@/lib/api/types.gen";
import { useT } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";

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
 * Brand and menus, the organization switcher, the search box and the two ways to start talking,
 * the conversation list, and the live line. On a phone it is the home screen itself (`screen`): the page heading, filling
 * the window.
 */
export function MessengerPanel({ me, screen = false }: { me: Me; screen?: boolean }) {
  const t = useT();
  const [query, setQuery] = useState("");
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
          <div className="flex items-center">
            <SearchButton onOpen={() => useLiveStore.getState().setPaletteOpen(true)} />
            <AccountMenu me={me} />
          </div>
        </div>
        <OrgHeader />
        <ConversationSearch value={query} onChange={setQuery} />
        <StartButtons />
        {screen ? <h1 className="sr-only">{t("conversations.label")}</h1> : null}
      </SidebarHeader>

      <SidebarContent className="min-h-0 flex-1 gap-0 overscroll-contain border-t border-sidebar-border">
        <ConversationList query={query} onClearQuery={() => setQuery("")} />
      </SidebarContent>

      <SidebarFooter className="border-t border-sidebar-border px-4 py-2.5">
        <LiveIndicator status={useLiveStore((s) => s.status)} className="text-sidebar-foreground/80" />
      </SidebarFooter>
    </div>
  );
}

/** The organization the switcher shows, with the gear that lists its pages beside it (SET-09). */
function OrgHeader() {
  const { org } = useCurrentOrg();
  return (
    <div className="flex items-center gap-2">
      <div className="min-w-0 flex-1">
        <OrgSwitcher />
      </div>
      {org ? <OrganizationMenu org={org} /> : null}
    </div>
  );
}

// flex-auto: each button first takes the width of its label, then they share what is left.
const START_BUTTON =
  "h-11 min-w-0 flex-auto border-sidebar-border bg-background/70 px-2 md:h-9 [&>span]:truncate";

/** "New conversation" and "New group" (LAY-03): the two ways to start talking. */
function StartButtons() {
  const t = useT();
  return (
    <div className="flex gap-2">
      <NewConversationDialog
        trigger={
          <Button variant="outline" className={START_BUTTON}>
            <MessageSquarePlusIcon aria-hidden="true" />
            <span>{t("conversations.new")}</span>
          </Button>
        }
      />
      <NewGroupDialog
        trigger={
          <Button variant="outline" className={START_BUTTON}>
            <UsersRoundIcon aria-hidden="true" />
            <span>{t("conversations.newGroup")}</span>
          </Button>
        }
      />
    </div>
  );
}
