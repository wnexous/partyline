import type { ReactNode } from "react";
import { AiStatusLine } from "@/components/ai-status";
import { BackToConversations } from "@/components/back-link";
import { IntelligenceAvatar } from "@/components/intelligence/intelligence-avatar";
import { IntelligenceName } from "@/components/intelligence/intelligence-name";
import { KindBadge } from "@/components/intelligence/kind-badge";
import { VisibilityLabel } from "@/components/room-visibility";
import { ConversationKindDM, type Room } from "@/lib/api/types.gen";
import { counterpartOf, groupFace } from "@/lib/conversations";
import { type Translate, useT } from "@/lib/i18n";

/** What a conversation is called: a group by its name, a DM by the other participant. */
export function conversationTitle(room: Room, viewerId: string, t: Translate): string {
  if (room.kind !== ConversationKindDM) return room.name;
  const other = counterpartOf(room, viewerId);
  if (other) return other.display_name;
  // A DM seen through an AI (its owner or a grantee) names both sides.
  const [a, b] = room.participants;
  return a && b ? t("conversation.between", { a: a.display_name, b: b.display_name }) : "";
}

/**
 * The platform of a DM with an external person, which is read-only history now (CID-21): null for
 * every other conversation and empty when the person carries no platform. People answer through
 * the connection that holds them, never in partyline.
 */
export function historyPlatform(room: Room): string | null {
  if (room.kind !== ConversationKindDM) return null;
  const person = room.participants.find((p) => p.role === "person");
  return person ? (person.platform ?? "") : null;
}

/**
 * The conversation page's header: avatar, title and kind badge; for a DM the counterpart's
 * @handle and, for an AI, whether it is online and in which app; for a group its visibility and
 * description. A DM the viewer only reads through an AI has no counterpart: both participants are
 * named, each with its own badge (PROF-09). `actions` are the page's buttons, `back` the way
 * back on a phone.
 */
export function ConversationHeader({
  room,
  viewerId,
  back,
  actions,
}: {
  room: Room;
  viewerId: string;
  back: boolean;
  actions: ReactNode;
}) {
  const t = useT();
  const title = conversationTitle(room, viewerId, t);
  const isDM = room.kind === ConversationKindDM;
  const counterpart = counterpartOf(room, viewerId);
  const face = isDM ? (counterpart ?? room.participants[0]) : undefined;
  const [first, second] = room.participants;
  const between = isDM && !counterpart && first && second ? ([first, second] as const) : null;

  return (
    <header
      data-testid="conversation-header"
      className="flex items-center gap-2 border-b px-4 py-2.5 md:px-6"
    >
      {back ? <BackToConversations className="-ml-3" /> : null}
      <IntelligenceAvatar
        intelligence={face ?? groupFace(room.id, room.name)}
        size="lg"
        decorative
        className={isDM ? undefined : "rounded-lg *:rounded-lg"}
      />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-2">
          {between ? (
            <h1 className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-lg tracking-[-0.015em]">
              <IntelligenceName intelligence={between[0]} viewerId={viewerId} />
              <span className="font-normal text-muted-foreground">{t("conversation.and")}</span>
              <IntelligenceName intelligence={between[1]} viewerId={viewerId} />
            </h1>
          ) : (
            <h1 className="truncate text-lg font-semibold tracking-[-0.015em]">{title}</h1>
          )}
          {counterpart ? (
            <KindBadge intelligence={counterpart} viewerId={viewerId} className="shrink-0" />
          ) : null}
        </div>
        <p className="flex min-w-0 items-center gap-x-2 text-sm text-muted-foreground">
          {isDM ? (
            counterpart ? (
              <>
                {counterpart.handle ? (
                  <span className="truncate font-mono text-xs">@{counterpart.handle}</span>
                ) : null}
                {counterpart.role === "ai" ? (
                  <AiStatusLine id={counterpart.id} appClassName="max-sm:hidden" />
                ) : null}
              </>
            ) : (
              <span className="truncate">{t("conversation.directMessage")}</span>
            )
          ) : (
            <>
              <VisibilityLabel visibility={room.visibility} className="shrink-0" />
              {room.description ? <span className="truncate">{room.description}</span> : null}
            </>
          )}
        </p>
      </div>
      {actions}
    </header>
  );
}
