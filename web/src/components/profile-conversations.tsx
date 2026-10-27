import { Link } from "@tanstack/react-router";
import { MessageCircleIcon } from "lucide-react";
import { ConversationKindDM, type RoomRef } from "@/lib/api/types.gen";
import { useT } from "@/lib/i18n";

/** The rows the profile panel shows before it sends the viewer to the full page for the rest. */
export const PANEL_CONVERSATIONS = 5;

/**
 * The conversations a profile lists (PEOP-03): the ones the server says the viewer can see, each
 * opening its room. A person's and an AI's profiles have them, and the full page paints them all;
 * the panel cuts the list to PANEL_CONVERSATIONS and links the page that lists the rest.
 */
export function ProfileConversations({
  conversations,
  limit,
  seeAllId,
}: {
  conversations: RoomRef[];
  /** How many rows to show; all of them when absent. */
  limit?: number;
  /** The profile whose page lists the rest; needed when `limit` cuts the list. */
  seeAllId?: string;
}) {
  const t = useT();
  if (conversations.length === 0) {
    return <p className="text-sm text-muted-foreground">{t("profile.noConversations")}</p>;
  }
  const shown = limit === undefined ? conversations : conversations.slice(0, limit);
  return (
    <>
      <ul aria-label={t("profile.conversations")} className="divide-y rounded-lg border">
        {shown.map((conversation) => (
          <li key={conversation.id}>
            <Link
              to="/rooms/$roomId"
              params={{ roomId: conversation.id }}
              className="flex min-h-11 items-center gap-3 px-4 py-3 text-sm outline-none hover:bg-accent/40 focus-visible:bg-accent/40 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
            >
              <MessageCircleIcon
                className="size-4 shrink-0 text-muted-foreground"
                aria-hidden="true"
              />
              <span className="min-w-0 truncate font-medium">
                {conversation.name || t("profile.directMessage")}
              </span>
              {conversation.kind === ConversationKindDM && conversation.name ? (
                <span className="shrink-0 text-xs text-muted-foreground">
                  {t("profile.directMessage")}
                </span>
              ) : null}
            </Link>
          </li>
        ))}
      </ul>
      {shown.length < conversations.length && seeAllId ? (
        <Link
          to="/contacts/$id"
          params={{ id: seeAllId }}
          className="mt-2 inline-flex min-h-11 items-center rounded-sm px-1 text-sm font-medium text-primary outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring"
        >
          {t("profilePanel.seeAll")}
        </Link>
      ) : null}
    </>
  );
}
