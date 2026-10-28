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
