import type { QueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/shared/api/query-keys";
import type { Conversation } from "@/shared/api/types.gen";

/** Replaces one conversation of the cached list; a list that is not cached stays absent. */
export function patchConversation(
  qc: QueryClient,
  roomId: string,
  patch: (c: Conversation) => Conversation,
) {
  qc.setQueryData<Conversation[]>(queryKeys.conversations, (old) =>
    old?.map((c) => (c.id === roomId ? patch(c) : c)),
  );
}

/**
 * The user read a room up to `seq`: only what lies after it can still be unread. The count never
 * goes up here, so an answer that arrives late cannot undo a newer one.
 */
export function markConversationRead(qc: QueryClient, roomId: string, seq: number) {
  patchConversation(qc, roomId, (c) => {
    const after = c.last_message ? Math.max(0, c.last_message.seq - seq) : 0;
    const unread = Math.min(c.unread, after);
    return unread === c.unread ? c : { ...c, unread };
  });
}
