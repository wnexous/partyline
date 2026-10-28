import { Link } from "@tanstack/react-router";
import {
  MessageSquarePlusIcon,
  MessagesSquareIcon,
  SearchIcon,
  UsersRoundIcon,
} from "lucide-react";

import { memo, useCallback, useMemo, useRef, useState } from "react";
import {
  ConversationMenu,
  type ConversationMenuTarget,
  hasConversationActions,
} from "@/components/conversation-menu";
import { NewConversationDialog } from "@/components/new-conversation-dialog";
import { NewGroupDialog } from "@/components/new-group-dialog";
import { ConversationAvatar, conversationMatches, useConversations } from "@/entities/room";
import { useCurrentOrg } from "@/hooks/use-current-org";
import { inOrg } from "@/lib/orgs";
import { isVoicePreview } from "@/lib/voice";
import type { Conversation } from "@/shared/api/types.gen";
import { useI18n, useT } from "@/shared/i18n";
import { useTouchFirst } from "@/shared/lib/device";
import { formatAbsolute, formatListTime } from "@/shared/lib/time";
import { useLongPress } from "@/shared/lib/use-long-press";
import { cn } from "@/shared/lib/utils";
import { EmptyState } from "@/shared/ui/molecules/empty-state";
import { ListSkeleton } from "@/shared/ui/molecules/list-skeleton";
import { RowMenuButton } from "@/shared/ui/molecules/row-menu-button";
import { Button } from "@/shared/ui/primitives/button";

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
 * offers: mark it read, its profile, leave and delete (ACT-01..ACT-04, components/conversation-menu).
 */

const FRESH_MS = 1_500;

/**
 * Rows whose last message is newer than the one this component showed before, for a moment.
 * Kept in a ref that render only extends, so a repeated render (StrictMode) says the same.
 */
function useFreshConversations(list: Conversation[] | undefined): Set<string> {
  const shown = useRef<Map<string, { seq: number; at: number }> | null>(null);
  const fresh = new Set<string>();
  if (!list) return fresh;
  if (shown.current === null) {
    shown.current = new Map(list.map((c) => [c.id, { seq: c.last_message?.seq ?? 0, at: 0 }]));
    return fresh;
  }
  const now = Date.now();
  for (const c of list) {
    const seq = c.last_message?.seq ?? 0;
    const known = shown.current.get(c.id);
    if (!known || seq > known.seq) shown.current.set(c.id, { seq, at: now });
    if (now - (shown.current.get(c.id)?.at ?? 0) < FRESH_MS) fresh.add(c.id);
  }
  return fresh;
}

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
      <ConversationMenu target={menuTarget} onClose={closeMenu} touch={touch} />
    </>
  );
}

/** Opens a row's actions: where, and which element takes focus back when they close. */
type MenuOpener = (
  conversation: Conversation,
  at: ConversationMenuTarget["at"],
  opener: HTMLElement,
) => void;

const ConversationRow = memo(function ConversationRow({
  conversation: c,
  fresh,
  touch,
  menuOpen,
  onMenu,
}: {
  conversation: Conversation;
  fresh: boolean;
  touch: boolean;
  menuOpen: boolean;
  onMenu: MenuOpener;
}) {
  const { t, locale } = useI18n();
  const last = c.last_message;
  const sender = last?.author_name ?? "";
  // A deleted last message previews nothing of what it said (DLT-10); one that is only a recording is
  // named as one, not by the file it made (VOI-04).
  const text = last?.deleted
    ? t("message.deleted")
    : last && isVoicePreview(last.text)
      ? t("conversations.voiceMessage")
      : (last?.text ?? "");
  // A row with nothing to offer is a plain link: no press, no button, the browser's own menu.
  const actionable = hasConversationActions(c);
  const press = useLongPress<HTMLAnchorElement>(
    (where) => onMenu(c, { x: where.x, y: where.y }, where.element),
    { enabled: actionable },
  );
  const withButton = actionable && !touch;

  return (
    // Rows far off screen skip layout and paint; a live message re-renders only its own row.
    <li className="group/row relative [contain-intrinsic-size:auto_4rem] [content-visibility:auto]">
      <Link
        to="/rooms/$roomId"
        params={{ roomId: c.id }}
        {...press}
        className={cn(
          "relative isolate grid grid-cols-[2.5rem_minmax(0,1fr)] items-center gap-x-3 px-3 py-2.5 outline-none transition-colors hover:bg-sidebar-accent focus-visible:bg-sidebar-accent focus-visible:ring-2 focus-visible:ring-sidebar-ring focus-visible:ring-inset data-[status=active]:bg-tape",
          // A long press must not select the name under the finger or call up the link's own preview.
          actionable && touch && "select-none [-webkit-touch-callout:none]",
        )}
      >
        {fresh ? (
          <span
            key={last?.seq}
            aria-hidden="true"
            className="pointer-events-none absolute inset-0 -z-10 animate-tape-settle"
          />
        ) : null}
        <ConversationAvatar conversation={c} size="lg" />
        <div className="min-w-0">
          <div className="flex items-baseline gap-2">
            <span className="min-w-0 flex-1 truncate font-semibold tracking-[-0.01em]">
              {c.title}
            </span>
            {last ? (
              <time
                dateTime={last.created_at}
                title={formatAbsolute(last.created_at, locale)}
                className={cn(
                  "shrink-0 font-mono text-[0.7rem] tabular",
                  // An unread row wears the accent twice: its count and its time.
                  c.unread > 0 ? "text-link" : "text-muted-foreground",
                  // The "⋯" takes the time's place while the row is hovered or has focus.
                  withButton && "group-focus-within/row:opacity-0 group-hover/row:opacity-0",
                )}
              >
                {formatListTime(last.created_at, locale)}
              </time>
            ) : null}
          </div>
          <div className="flex items-center gap-2">
            <span
              className={
                c.unread > 0
                  ? "min-w-0 flex-1 truncate text-sm text-foreground/85"
                  : "min-w-0 flex-1 truncate text-sm text-muted-foreground"
              }
            >
              {last
                ? sender && text
                  ? `${sender}: ${text}`
                  : text
                : t("conversations.noMessages")}
            </span>
            {c.unread > 0 ? <UnreadBadge count={c.unread} /> : null}
          </div>
        </div>
      </Link>
      {withButton ? (
        <RowMenuButton
          label={t("actions.conversation", { name: c.title })}
          expanded={menuOpen}
          onOpen={(at, button) => onMenu(c, at, button)}
          className="absolute top-2.5 right-3 opacity-0 group-focus-within/row:opacity-100 group-hover/row:opacity-100 aria-expanded:opacity-100"
        />
      ) : null}
    </li>
  );
});

function UnreadBadge({ count }: { count: number }) {
  const t = useT();
  return (
    <span className="inline-flex h-5 min-w-5 shrink-0 items-center justify-center rounded-full bg-primary px-1.5 text-[0.7rem] font-semibold text-primary-foreground tabular">
      <span aria-hidden="true">{count > 99 ? "99+" : count}</span>
      <span className="sr-only">
        {t(count === 1 ? "conversations.unreadOne" : "conversations.unreadMany", { count })}
      </span>
    </span>
  );
}
