import { create } from "zustand";

/*
 * The live socket's transport state: whether the line is open and since when it has been down.
 * It carries no business knowledge, so every layer may read it (the banner, the offline notice).
 */

export type SocketStatus = "connecting" | "open" | "closed";

export interface SocketState {
  status: SocketStatus;
  /** Since when the socket has not been open (ms since epoch); null while open or idle. */
  offlineSince: number | null;
  setStatus: (status: SocketStatus) => void;
  /** Restarts the offline grace after the page was hidden, unless the socket is open. */
  markVisible: () => void;
}

export const useSocketStore = create<SocketState>()((set) => ({
  status: "closed",
  offlineSince: null,

  setStatus: (status) =>
    set((s) => {
      if (s.status === status) return s;
      if (status === "open") return { status, offlineSince: null };
      // Retries flip between connecting and closed; the outage started at the first drop.
      return { status, offlineSince: s.offlineSince ?? Date.now() };
    }),
  markVisible: () =>
    set((s) => {
      // The grace counts while the page is showing: a tab that comes back gets a fresh 2 s before
      // the banner (UI-06), since the outage timestamp it carries was set while nobody was looking.
      // An idle store (nothing started, no outage) has no grace to restart.
      if (s.status === "open" || s.offlineSince === null) return s;
      return { offlineSince: Date.now() };
    }),
}));
