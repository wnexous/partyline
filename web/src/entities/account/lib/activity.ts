import { type ActivityItem, DefaultActivityLimit } from "@/shared/api/types.gen";

/** Feed entries requested, then kept in the dashboard feed in total and at most per room. */
export const ACTIVITY_FETCH = DefaultActivityLimit;

export const ACTIVITY_SIZE = 8;

export const ACTIVITY_PER_ROOM = 3;

/** Newest message first; seq breaks ties inside a room. */
export function newestFirst(a: ActivityItem, b: ActivityItem): number {
  const diff = Date.parse(b.message.created_at) - Date.parse(a.message.created_at);
  return diff !== 0 ? diff : b.message.seq - a.message.seq;
}

/**
 * Shapes the dashboard feed: newest first, no duplicate ids, at most ACTIVITY_PER_ROOM messages
 * per room (one busy room must not hide the others), at most ACTIVITY_SIZE in total.
 */
export function trimActivity(entries: ActivityItem[]): ActivityItem[] {
  const seen = new Set<string>();
  const perRoom = new Map<string, number>();
  const out: ActivityItem[] = [];
  for (const entry of [...entries].sort(newestFirst)) {
    const m = entry.message;
    if (seen.has(m.id)) continue;
    const n = perRoom.get(m.room_id) ?? 0;
    if (n >= ACTIVITY_PER_ROOM) continue;
    seen.add(m.id);
    perRoom.set(m.room_id, n + 1);
    out.push(entry);
    if (out.length === ACTIVITY_SIZE) break;
  }
  return out;
}
