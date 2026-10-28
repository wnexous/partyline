import type { QueryClient } from "@tanstack/react-query";
import { patchList } from "@/shared/api/patch-list";
import { queryKeys } from "@/shared/api/query-keys";
import type { Agent, Contacts, LiveEvent } from "@/shared/api/types.gen";
import { knowsAI } from "../lib/contacts";
import type { Presence } from "./presence-store";

/*
 * What the live frames do to an intelligence's cached data: its presence, its row in the agents
 * list, the contacts and its profile.
 */

type AgentPresenceFrame = Extract<LiveEvent, { type: "agent.presence" }>["data"];

/**
 * An AI's presence changed (agent.presence): the store knows it at once, its agents row and its
 * profile are fetched again when cached, and the contacts are refetched when they lack the AI.
 */
export function agentPresenceChanged(
  qc: QueryClient,
  setPresence: (agentId: string, presence: Presence) => void,
  frame: AgentPresenceFrame,
) {
  const { agent_id, online, last_seen_at } = frame;
  setPresence(agent_id, { online, last_seen_at });
  patchList<Agent>(qc, queryKeys.agents, agent_id, (a) => ({
    ...a,
    online,
    last_seen_at: last_seen_at ?? a.last_seen_at,
  }));
  // Agents are created on their first connection: an unknown id means the list is stale.
  const cached = qc.getQueryData<Agent[]>(queryKeys.agents);
  if (cached && !cached.some((a) => a.id === agent_id)) {
    qc.invalidateQueries({ queryKey: queryKeys.agents, exact: true });
  }
  // The contacts overlay the live state themselves; one that lacks the AI is out of date.
  const contacts = qc.getQueryData<Contacts>(queryKeys.contacts);
  if (contacts && !knowsAI(contacts, agent_id)) {
    qc.invalidateQueries({ queryKey: queryKeys.contacts, exact: true });
  }
  // A session that came or went changes what the profile and the mention list show (SESS-32,
  // SESS-33): the profile is fetched again when it is open, and the room members carry the
  // sessions next to the online flag.
  if (qc.getQueryData(queryKeys.intelligence(agent_id))) {
    qc.invalidateQueries({ queryKey: queryKeys.intelligence(agent_id), exact: true });
  }
}
