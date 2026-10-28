import type { AIContact } from "@/shared/api/types.gen";
import { type Presence, usePresenceStore } from "./presence-store";

/**
 * An AI of the contacts with its live presence folded in (the store wins over the fetched list):
 * the contacts hold who is online when they were fetched, `agent.presence` frames keep it current.
 */
export function withContactPresence(contact: AIContact, presence: Presence | undefined): AIContact {
  if (!presence) return contact;
  const online = presence.online;
  const lastSeen = presence.last_seen_at ?? contact.last_seen_at;
  if (online === contact.online && lastSeen === contact.last_seen_at) return contact;
  return { ...contact, online, ...(lastSeen ? { last_seen_at: lastSeen } : {}) };
}

/**
 * Whether an intelligence is online: the live presence frames win, then `fallback` (the value that
 * came with the data being shown: a profile, a member, a contact), then offline. The one rule for
 * every place that shows a presence dot (WA-06); outside a component, read it with `onlineOf`.
 */
export function onlineOf(
  presence: Record<string, Presence>,
  id: string,
  fallback?: boolean,
): boolean {
  return presence[id]?.online ?? fallback ?? false;
}

export function useIsOnline(id: string, fallback?: boolean): boolean {
  return usePresenceStore((s) => onlineOf(s.presence, id, fallback));
}
