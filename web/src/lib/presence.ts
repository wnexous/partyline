import type { Agent } from "@/lib/api/types.gen";
import type { Presence } from "@/lib/live/store";

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
