import type { IntelligenceRef, Room } from "@/lib/api/types.gen";
import { ConversationKindDM } from "@/lib/api/types.gen";

/** A group has no profile: its initials and colour come from its name and room id. */
export function groupFace(id: string, title: string): IntelligenceRef {
  return { id, kind: "human", role: "user", display_name: title, handle: "" };
}

/** The other participant of a DM the viewer takes part in; undefined for a group or a DM viewed through an AI. */
export function counterpartOf(room: Room, viewerId: string): IntelligenceRef | undefined {
  if (room.kind !== ConversationKindDM) return undefined;
  if (!room.participants.some((p) => p.id === viewerId)) return undefined;
  return room.participants.find((p) => p.id !== viewerId);
}
