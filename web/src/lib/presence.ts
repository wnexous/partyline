import type { AIContact } from "@/lib/api/types.gen";
import type { Presence } from "@/lib/live/store";

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
