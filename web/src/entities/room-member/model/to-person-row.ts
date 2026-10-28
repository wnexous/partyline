import {
  IntelligenceKindAI,
  IntelligenceKindHuman,
  type IntelligenceRef,
  type IntelligenceRole,
  MemberKindAgent,
  MemberKindUser,
  type RoomMember,
  type SessionInfo,
} from "@/lib/api/types.gen";

/** What a person of a conversation is, whichever way the server named it. */
export type PersonKind = "ai" | "user" | "person";

/**
 * One person or AI of a conversation as a list shows it. A group lists room members (told apart by
 * `kind`: agent, user, chat) and a DM lists its two participants (told apart by `role`: ai, user,
 * person); this is the one shape both are read through.
 */
export interface PersonRow {
  /** The key and test id of the row: the member row's id, or the participant's own id. */
  rowId: string;
  /** The id of the intelligence: what presence and profiles are keyed by. */
  id: string;
  kind: PersonKind;
  name: string;
  intelligence: IntelligenceRef;
  /** Presence and sessions as the server sent them with a member; a participant carries none. */
  online?: boolean;
  sessions?: SessionInfo[];
  /** The room member behind the row, for what only a group has (settings, remove, owner). */
  member?: RoomMember;
}

const isMember = (source: RoomMember | IntelligenceRef): source is RoomMember =>
  "member_id" in source;

/** A member the server sent without its profile still gets a face: its name and no handle. */
function stand(member: RoomMember, kind: PersonKind): IntelligenceRef {
  const role: IntelligenceRole = kind === "ai" ? "ai" : "user";
  return {
    id: member.member_id,
    kind: kind === "ai" ? IntelligenceKindAI : IntelligenceKindHuman,
    role,
    display_name: member.name,
    handle: "",
  };
}

/**
 * Reads a room member or a DM participant into a `PersonRow`. A chat is no intelligence and is
 * never a row (CID-20): it comes back as null, as does a member of a kind this app does not know.
 */
export function toPersonRow(source: RoomMember | IntelligenceRef): PersonRow | null {
  if (isMember(source)) {
    const kind =
      source.kind === MemberKindAgent ? "ai" : source.kind === MemberKindUser ? "user" : null;
    if (kind === null) return null;
    return {
      rowId: source.id,
      id: source.member_id,
      kind,
      name: source.name,
      intelligence: source.intelligence ?? stand(source, kind),
      ...(source.online !== undefined ? { online: source.online } : {}),
      ...(source.sessions ? { sessions: source.sessions } : {}),
      member: source,
    };
  }
  return {
    rowId: source.id,
    id: source.id,
    kind: source.role,
    name: source.display_name.trim() || source.handle,
    intelligence: source,
  };
}
