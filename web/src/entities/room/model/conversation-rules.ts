import {
  type Conversation,
  ConversationKindDM,
  ConversationKindGroup,
  LevelManage,
  LevelPost,
  type Room,
} from "@/shared/api/types.gen";
import type { Translate } from "@/shared/i18n";
import { counterpartOf } from "../lib/conversations";

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
 * Whether a conversation is read-only history: a DM with an external person (its platform), or a post
 * the server refused as history (CID-21). Nothing is sent or edited in it.
 */
export function isHistoryOnly(room: Room, refusedAsHistory: boolean): boolean {
  return historyPlatform(room) !== null || refusedAsHistory;
}

export const managesGroup = (c: Conversation) =>
  c.kind !== ConversationKindDM && c.my_level === LevelManage;

/** A member of a group is one with a `post` level: the member row is what gives it (AD-016). */
const memberOfGroup = (c: Conversation) =>
  c.kind === ConversationKindGroup && c.my_level === LevelPost;

/** Whether a conversation has anything to offer: a row with nothing leaves the browser's own menu alone. */
export function hasConversationActions(c: Conversation): boolean {
  return (
    c.unread > 0 ||
    (c.kind === ConversationKindDM && c.counterpart !== undefined) ||
    managesGroup(c) ||
    // Leaving needs no unread and no manage: a plain member may always leave a group (ROOM-01).
    memberOfGroup(c)
  );
}
