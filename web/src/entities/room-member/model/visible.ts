import type { IntelligenceRef, RoomMember } from "@/shared/api/types.gen";
import { type PersonRow, toPersonRow } from "./to-person-row";

/**
 * The rows a member panel shows: one per room member or DM participant the adapter can read. Chats
 * are never members (CID-20, AD-015), so a chat row left in old data is not rendered.
 */
export function visibleRows(sources: Parameters<typeof toPersonRow>[0][]): PersonRow[] {
  return sources
    .map((source) => toPersonRow(source))
    .filter((row): row is PersonRow => row !== null);
}

/**
 * The people of a group: whoever holds it first, then its members (GRP-05). The owner reads and
 * writes a group without a member row, so without this row nobody could see who holds it and the
 * count was one short. A member row of the owner, left in old data, is not repeated.
 */
export function groupRows(members: RoomMember[], owner?: IntelligenceRef): PersonRow[] {
  const rows = visibleRows(members);
  if (!owner) return rows;
  const ownerRow: PersonRow = { ...(toPersonRow(owner) as PersonRow), owner: true };
  return [ownerRow, ...rows.filter((row) => row.id !== owner.id)];
}
