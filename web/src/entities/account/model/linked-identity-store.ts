import { create } from "zustand";
import type { Identity } from "@/shared/api/types.gen";

export interface LinkedIdentityState {
  /** The most recent identity.linked event, for the pairing dialog (UI-18). */
  lastLinkedIdentity: Identity | null;
  setLastLinkedIdentity: (identity: Identity | null) => void;
}

export const useLinkedIdentityStore = create<LinkedIdentityState>()((set) => ({
  lastLinkedIdentity: null,
  setLastLinkedIdentity: (identity) => set({ lastLinkedIdentity: identity }),
}));
