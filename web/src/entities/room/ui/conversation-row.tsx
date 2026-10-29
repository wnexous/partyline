import { Link } from "@tanstack/react-router";
import { memo } from "react";
import { isVoicePreview } from "@/entities/message/@x/room";
import type { Conversation } from "@/shared/api/types.gen";
import { useI18n, useT } from "@/shared/i18n";
import { useLongPress } from "@/shared/lib/use-long-press";
import { cn } from "@/shared/lib/utils";
import { RelativeTime } from "@/shared/ui/atoms/relative-time";
import { RowMenuButton } from "@/shared/ui/molecules/row-menu-button";
import { hasConversationActions } from "../model/conversation-rules";
import { ConversationAvatar } from "./conversation-avatar";

/** Where a row's menu hangs: the pointer, or the edge of the button that opened it. */
export interface RowMenuPlace {
  x: number;
  y: number;
  align?: "start" | "end";
}

/** Opens a row's actions: where, and which element takes focus back when they close. */
export type MenuOpener = (
  conversation: Conversation,
  at: RowMenuPlace,
  opener: HTMLElement,
) => void;

export const ConversationRow = memo(function ConversationRow({
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
        <ConversationAvatar variant="conversation" conversation={c} size="lg" />
        <div className="min-w-0">
          <div className="flex items-baseline gap-2">
            <span className="min-w-0 flex-1 truncate font-semibold tracking-[-0.01em]">
              {c.title}
            </span>
            {last ? (
              <RelativeTime
                value={last.created_at}
                locale={locale}
                format="list"
                className={cn(
                  "shrink-0 font-mono text-[0.7rem] tabular",
                  // An unread row wears the accent twice: its count and its time.
                  c.unread > 0 ? "text-link" : "text-muted-foreground",
                  // The "⋯" takes the time's place while the row is hovered or has focus.
                  withButton && "group-focus-within/row:opacity-0 group-hover/row:opacity-0",
                )}
              />
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
