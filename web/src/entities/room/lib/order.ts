import type { Conversation } from "@/shared/api/types.gen";

/** Newest activity first; ties by title (case-insensitive), then id: the server's own order. */
export function newestActivityFirst(a: Conversation, b: Conversation): number {
  const diff = Date.parse(b.activity_at) - Date.parse(a.activity_at);
  if (diff !== 0) return diff;
  const [ta, tb] = [a.title.toLowerCase(), b.title.toLowerCase()];
  if (ta !== tb) return ta < tb ? -1 : 1;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}
