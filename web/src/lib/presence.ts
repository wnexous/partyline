import type { Agent, AIContact } from "@/lib/api/types.gen";
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

/** An agent with its live presence folded in (the store wins over the fetched list). */
export interface AgentView extends Agent {
  live: boolean;
  seen?: string;
}

export function withPresence(agent: Agent, presence: Presence | undefined): AgentView {
  return {
    ...agent,
    live: presence?.online ?? agent.online,
    seen: presence?.last_seen_at ?? agent.last_seen_at,
  };
}

/** Online first, then most recently seen, then never connected; ties by name. */
export function byLiveness(a: AgentView, b: AgentView): number {
  if (a.live !== b.live) return a.live ? -1 : 1;
  const seenA = a.seen ? Date.parse(a.seen) : 0;
  const seenB = b.seen ? Date.parse(b.seen) : 0;
  if (seenA !== seenB) return seenB - seenA;
  return a.name.localeCompare(b.name);
}
