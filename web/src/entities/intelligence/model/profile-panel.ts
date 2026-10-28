import { useMemo } from "react";
import { create } from "zustand";

/*
 * Which profile the panel shows (PRF-01, PRF-02). The panel itself is mounted once, in the app
 * shell (widgets/profile-panel/ui/profile-panel.tsx); anything that shows an identity opens it through
 * `useProfilePanel().open(id)`.
 */

interface ProfilePanelState {
  /** The intelligence whose profile is open; null while the panel is closed. */
  id: string | null;
  /** The element that opened it, which takes focus back when the panel closes (PRF-05). */
  opener: HTMLElement | null;
  open: (id: string, opener?: HTMLElement | null) => void;
  /** Closes the panel; focus goes back to the element that opened it. */
  close: () => void;
  /** Closes it for good, as a new page does: nothing gets focus back. */
  dismiss: () => void;
}

const focused = () =>
  document.activeElement instanceof HTMLElement ? document.activeElement : null;

export const useProfilePanelStore = create<ProfilePanelState>()((set) => ({
  id: null,
  opener: null,
  // Opening another identity while the panel is up keeps the element that first opened it.
  open: (id, opener) =>
    set((state) => ({ id, opener: state.id === null ? (opener ?? focused()) : state.opener })),
  close: () => set({ id: null }),
  dismiss: () => set({ id: null, opener: null }),
}));

/**
 * The one way to open the profile panel: `useProfilePanel().open(id)` shows the profile of the
 * intelligence with that id. `opener` is the element to give focus back to when the panel closes;
 * a click handler passes its `currentTarget` (some browsers do not focus a clicked link), and
 * without one the focused element at that moment is used.
 */
export function useProfilePanel() {
  const open = useProfilePanelStore((state) => state.open);
  const close = useProfilePanelStore((state) => state.close);
  return useMemo(() => ({ open, close }), [open, close]);
}
