import type { ReactNode } from "react";
import { AiStatusLine } from "@/components/ai-status";
import { GroupAvatar } from "@/components/group-avatar";
import { ProfileLink } from "@/components/profile-link";
import { IntelligenceAvatar, IntelligenceName } from "@/entities/intelligence";
import { counterpartOf } from "@/lib/conversations";
import { ConversationKindDM, type Room } from "@/shared/api/types.gen";
import { type Translate, useT } from "@/shared/i18n";
import { BackToConversations } from "@/shared/ui/molecules/back-link";

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
 * The conversation page's header: avatar and title; for a DM the counterpart's @handle and, for
 * an AI, whether it is online and in which app (the face and the name open its profile panel,
 * PRF-01); for a group its description. A DM the viewer only reads through an AI has no
 * counterpart: both participants are named (PROF-09). Nothing beside the title says whether the
 * counterpart is an AI or a person (AD-024). `actions` are the page's buttons, `back` the way back
 * on a phone.
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
      className="flex flex-wrap items-center gap-2 border-b px-4 py-2.5 md:px-6"
    >
      {back ? <BackToConversations className="-ml-3" /> : null}
      {face ? (
        // The face of a DM opens that intelligence's profile panel (PRF-01); a group has no profile.
        <ProfileLink
          id={face.id}
          aria-label={t("message.openProfile", { name: face.display_name })}
          className="shrink-0 rounded-full focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
        >
          <IntelligenceAvatar intelligence={face} size="lg" decorative />
        </ProfileLink>
      ) : (
        <GroupAvatar
          group={{ id: room.id, name: room.name, avatarUrl: room.avatar_url }}
          size="lg"
          decorative
        />
      )}
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-2">
          {between ? (
            <h1 className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-lg tracking-[-0.015em]">
              <IntelligenceName intelligence={between[0]} viewerId={viewerId} profileLink />
              <span className="font-normal text-muted-foreground">{t("conversation.and")}</span>
              <IntelligenceName intelligence={between[1]} viewerId={viewerId} profileLink />
            </h1>
          ) : (
            <h1 className="truncate text-lg font-semibold tracking-[-0.015em]">
              {counterpart ? (
                <ProfileLink
                  id={counterpart.id}
                  className="rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
                >
                  {title}
                </ProfileLink>
              ) : (
                title
              )}
            </h1>
          )}
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
          ) : room.description ? (
            <span className="truncate">{room.description}</span>
          ) : null}
        </p>
      </div>
      {actions ? <div className="ml-auto flex shrink-0 items-center gap-2">{actions}</div> : null}
    </header>
  );
}
