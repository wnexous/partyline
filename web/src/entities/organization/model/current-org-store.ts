import { create } from "zustand";
import { CURRENT_ORG_KEY } from "@/lib/orgs";
import { readStorage, writeStorage } from "@/lib/theme";

export interface CurrentOrgState {
  /**
   * The organization the viewer picked in this browser (ORG-02); null until they pick one. What it
   * means is `useCurrentOrg`'s to say: the pick of an organization they left counts for nothing.
   */
  currentOrgId: string | null;
  setCurrentOrg: (orgId: string | null) => void;
}

export const useCurrentOrgStore = create<CurrentOrgState>()((set) => ({
  currentOrgId: readStorage(CURRENT_ORG_KEY),
  setCurrentOrg: (currentOrgId) => {
    writeStorage(CURRENT_ORG_KEY, currentOrgId);
    set({ currentOrgId });
  },
}));
