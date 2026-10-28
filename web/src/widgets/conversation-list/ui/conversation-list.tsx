import {
  MessageSquarePlusIcon,
  MessagesSquareIcon,
  SearchIcon,
  UsersRoundIcon,
} from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { useProfilePanel } from "@/entities/intelligence";
import { inOrg, useCurrentOrg } from "@/entities/organization";
import {
  ConversationRow,
  conversationMatches,
  type MenuOpener,
  useConversations,
} from "@/entities/room";
import { ConversationMenu, type ConversationMenuTarget } from "@/features/conversation-actions";
import { NewGroupDialog } from "@/features/create-group";
import { NewConversationDialog } from "@/features/start-conversation";
import { useT } from "@/shared/i18n";
import { useTouchFirst } from "@/shared/lib/device";
import { EmptyState } from "@/shared/ui/molecules/empty-state";
import { ListSkeleton } from "@/shared/ui/molecules/list-skeleton";
import { Button } from "@/shared/ui/primitives/button";
import { useFreshConversations } from "../model/use-fresh-conversations";

/*
 * The sidebar's conversation list (LAY-01, DM-06): direct messages and groups together, newest
 * activity first as the server and the live handlers keep it, each row with avatar, name, last
 * message, time and unread count. A row that just received a message lands on tape that settles
 * into the paper, like a new line in the timeline.
 *
 * The list is the current organization's (ORG-02): its groups, and each DM filed under it. While
 * the viewer's organizations are not known it lists everything, so an unreachable profile never
 * hides a conversation.
 *
 * A long press (touch), a right click or the "⋯" on a row (mouse) opens what the conversation
 * offers: mark it read, its profile, leave and delete (ACT-01..ACT-04, features/conversation-actions).
 */

export function ConversationList({
  query = "",
  onClearQuery,
}: {
  /** What the sidebar search box holds: only the conversations it finds are listed (LAY-03). */
  query?: string;
  onClearQuery?: () => void;
}) {
  const t = useT();
  const conversations = useConversations();
  const { org, loading: orgLoading } = useCurrentOrg();
  const touch = useTouchFirst();
  const profile = useProfilePanel();
  const [menu, setMenu] = useState<ConversationMenuTarget | null>(null);
  // The menu shows the row as it is now (a message may have come in), not as it was when it opened.
  const menuTarget = useMemo(
    () =>
      menu && {
        ...menu,
        conversation:
          conversations.data?.find((x) => x.id === menu.conversation.id) ?? menu.conversation,
      },
    [menu, conversations.data],
  );
  const openMenu = useCallback<MenuOpener>(
    (conversation, at, opener) => setMenu({ conversation, at, opener }),
    [],
  );
  const closeMenu = useCallback(() => setMenu(null), []);
  // Tracked on the whole list, so a hidden conversation that gets a message settles when shown.
  const fresh = useFreshConversations(conversations.data);
  const text = query.trim();
  const orgId = org?.id;
  const inCurrentOrg = useMemo(
    () => (orgId ? (conversations.data ?? []).filter((c) => inOrg(c, orgId)) : conversations.data),
    [conversations.data, orgId],
  );
  const shown = useMemo(
    () => (text ? (inCurrentOrg ?? []).filter((c) => conversationMatches(c, text)) : null),
    [inCurrentOrg, text],
  );

  if (conversations.isPending || orgLoading) return <ListSkeleton rows={5} avatar />;

  // A refresh that fails keeps the list on screen; only a list that never loaded is an error.
  if (conversations.isError && !conversations.data) {
    return (
      <div role="alert" className="px-4 py-6 text-sm">
        <p className="text-destructive">{t("conversations.loadError")}</p>
        <Button
          variant="outline"
          size="sm"
          className="mt-3"
          onClick={() => void conversations.refetch()}
        >
          {t("common.retry")}
        </Button>
      </div>
    );
  }

  if (!conversations.data || !inCurrentOrg || inCurrentOrg.length === 0) {
    // Conversations of the other organizations are not lost: say whose list this is.
    const elsewhere =
      org !== undefined && conversations.data !== undefined && conversations.data.length > 0;
    return (
      <EmptyState
        icon={MessagesSquareIcon}
        title={
          elsewhere
            ? t("conversations.emptyInOrg", { org: org.name })
            : t("conversations.emptyTitle")
        }
        // The list sits beside the page, before its heading: a heading here would be out of order.
        headingLevel="p"
        className="py-8 md:py-8"
        action={
          <>
            <NewConversationDialog
              trigger={
                <Button>
                  <MessageSquarePlusIcon aria-hidden="true" />
                  {t("conversations.new")}
                </Button>
              }
            />
            <NewGroupDialog
              trigger={
                <Button variant="outline">
                  <UsersRoundIcon aria-hidden="true" />
                  {t("conversations.newGroup")}
                </Button>
              }
            />
          </>
        }
      >
        <p>{t("conversations.emptyBody")}</p>
      </EmptyState>
    );
  }

  const rows = shown ?? inCurrentOrg;
  return (
    <>
      {/* Heard by screen readers as the search narrows the list; the nothing-found case speaks for itself. */}
      <p role="status" className="sr-only">
        {text && rows.length > 0
          ? t(rows.length === 1 ? "conversations.foundOne" : "conversations.foundMany", {
              count: rows.length,
            })
          : ""}
      </p>
      {rows.length > 0 ? (
        <ul aria-label={t("conversations.label")} className="pb-2">
          {rows.map((c) => (
            <ConversationRow
              key={c.id}
              conversation={c}
              fresh={fresh.has(c.id)}
              touch={touch}
              menuOpen={menu?.conversation.id === c.id}
              onMenu={openMenu}
            />
          ))}
        </ul>
      ) : (
        <p role="status" className="px-4 pt-6 pb-2 text-sm text-muted-foreground">
          {t("conversations.noMatch", { query: text })}
        </p>
      )}
      {text ? (
        <div className="flex flex-col items-start gap-1 px-1 pt-1 pb-3">
          {rows.length === 0 && onClearQuery ? (
            <Button variant="ghost" size="sm" onClick={onClearQuery}>
              {t("conversations.showAll")}
            </Button>
          ) : null}
          <NewConversationDialog
            initialQuery={text}
            trigger={
              <Button variant="ghost" size="sm">
                <SearchIcon aria-hidden="true" />
                {t("conversations.findContact", { query: text })}
              </Button>
            }
          />
        </div>
      ) : null}
      <ConversationMenu
        target={menuTarget}
        onClose={closeMenu}
        touch={touch}
        onViewProfile={profile.open}
      />
    </>
  );
}
