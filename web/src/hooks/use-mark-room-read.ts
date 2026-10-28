import { useEffect, useRef } from "react";
import { useMarkRead, useNewestSeq } from "@/lib/api/queries";
import { useDocumentVisible } from "@/shared/lib/use-document-visible";

/**
 * Reads the conversation up to its newest message when it opens and again for each new message,
 * while the tab is visible (DM-07). Each newest seq is sent once; an empty conversation has
 * nothing to read; a hidden tab reads nothing, and what arrived meanwhile is read on showing.
 */
export function useMarkRoomRead(roomId: string): void {
  const visible = useDocumentVisible();
  const newest = useNewestSeq(roomId);
  const { mutate } = useMarkRead();
  const sent = useRef<{ roomId: string; seq: number } | null>(null);

  useEffect(() => {
    if (!visible || newest === undefined) return;
    const last = sent.current;
    if (last && last.roomId === roomId && last.seq >= newest) return;
    sent.current = { roomId, seq: newest };
    mutate({ roomId, seq: newest });
  }, [roomId, visible, newest, mutate]);
}
