import { useRef } from "react";
import type { Conversation } from "@/shared/api/types.gen";

/*
 * What is fresh in the list: rows whose last message is newer than the one this list showed before,
 * for a moment (the row settles in like a new line in the timeline).
 */

const FRESH_MS = 1_500;

/**
 * Rows whose last message is newer than the one this component showed before, for a moment.
 * Kept in a ref that render only extends, so a repeated render (StrictMode) says the same.
 */
export function useFreshConversations(list: Conversation[] | undefined): Set<string> {
  const shown = useRef<Map<string, { seq: number; at: number }> | null>(null);
  const fresh = new Set<string>();
  if (!list) return fresh;
  if (shown.current === null) {
    shown.current = new Map(list.map((c) => [c.id, { seq: c.last_message?.seq ?? 0, at: 0 }]));
    return fresh;
  }
  const now = Date.now();
  for (const c of list) {
    const seq = c.last_message?.seq ?? 0;
    const known = shown.current.get(c.id);
    if (!known || seq > known.seq) shown.current.set(c.id, { seq, at: now });
    if (now - (shown.current.get(c.id)?.at ?? 0) < FRESH_MS) fresh.add(c.id);
  }
  return fresh;
}
