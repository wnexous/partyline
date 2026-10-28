import { create } from "zustand";

export interface OpenRoomState {
  /**
   * The conversation the user is looking at: its page is open and the tab is visible. Messages
   * that arrive in it are read at once, so they add no unread (LAY-02).
   */
  openRoomId: string | null;
  setOpenRoom: (roomId: string | null) => void;
}

export const useOpenRoomStore = create<OpenRoomState>()((set) => ({
  openRoomId: null,
  setOpenRoom: (openRoomId) => set({ openRoomId }),
}));
