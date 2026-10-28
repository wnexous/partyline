import type { Conversation, IntelligenceRef, Room } from "@/shared/api/types.gen";
import { ConversationKindDM } from "@/shared/api/types.gen";
import { matchesTerms } from "@/shared/lib/search";

/**
 * A group has no profile: its initials and colour come from its name and room id, and its picture
 * is the photo its managers gave it, when they did (GAV-01).
 */
export function groupFace(id: string, title: string, avatarUrl?: string): IntelligenceRef {
  const face: IntelligenceRef = {
    id,
    kind: "human",
    role: "user",
    display_name: title,
    handle: "",
  };
  return avatarUrl ? { ...face, avatar_url: avatarUrl } : face;
}

/** The other participant of a DM the viewer takes part in; undefined for a group or a DM viewed through an AI. */
export function counterpartOf(room: Room, viewerId: string): IntelligenceRef | undefined {
  if (room.kind !== ConversationKindDM) return undefined;
  if (!room.participants.some((p) => p.id === viewerId)) return undefined;
  return room.participants.find((p) => p.id !== viewerId);
}

/**
 * Whether the sidebar search finds the conversation (LAY-03): its name, or the display name or
 * @handle of one of its users and AIs. Every word has to be found, each in either place.
 */
export function conversationMatches(c: Conversation, query: string): boolean {
  return matchesTerms([c.title, ...c.member_names].join("\n"), query);
}
