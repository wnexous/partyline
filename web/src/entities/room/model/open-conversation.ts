import { useEffect } from "react";
import { useDocumentVisible } from "@/shared/lib/use-document-visible";
import { useOpenRoomStore } from "./open-room-store";

/**
 * Marks `roomId` as the conversation being looked at while its page is mounted and the tab is
 * visible (LAY-02): messages that arrive in it add no unread. A hidden tab reads nothing.
 */
export function useOpenConversation(roomId: string): void {
  const visible = useDocumentVisible();
  const setOpenRoom = useOpenRoomStore((s) => s.setOpenRoom);
  useEffect(() => {
    if (!visible) return;
    setOpenRoom(roomId);
    return () => setOpenRoom(null);
  }, [roomId, visible, setOpenRoom]);
}
