import { create } from "zustand";

/*
 * Who is online right now, as the live socket pushes it (agent.presence). The profile and contact
 * data say the same at fetch time; `useIsOnline` reads this first.
 */

export interface Presence {
  online: boolean;
  last_seen_at?: string;
}

export interface PresenceState {
  presence: Record<string, Presence>;
  setPresence: (agentId: string, presence: Presence) => void;
}

export const usePresenceStore = create<PresenceState>()((set) => ({
  presence: {},
  setPresence: (agentId, presence) =>
    set((s) => ({ presence: { ...s.presence, [agentId]: presence } })),
}));
