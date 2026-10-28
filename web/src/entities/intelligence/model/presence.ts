import { usePresenceStore } from "./presence-store";

/** The pure rule for the contacts list: the live frame wins over the fetched list. */
export { withContactPresence } from "@/lib/presence";

/**
 * Whether an intelligence is online: the live presence frames win, then `fallback` (the value that
 * came with the data being shown: a profile, a member, a contact), then offline. The one rule for
 * every place that shows a presence dot (WA-06).
 */
export function useIsOnline(id: string, fallback?: boolean): boolean {
  return usePresenceStore((s) => s.presence[id]?.online ?? fallback ?? false);
}
