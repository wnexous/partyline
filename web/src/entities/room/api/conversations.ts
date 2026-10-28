import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import type { Conversation, List, MarkReadRequest, ReadState } from "@/shared/api/types.gen";
import { patchConversation } from "../model/conversations-cache";

/** The caller's DMs and groups, newest activity first, with unread counts (GET /conversations). */
export const conversationsQuery = () =>
  queryOptions({
    queryKey: queryKeys.conversations,
    queryFn: ({ signal }) =>
      api<List<Conversation>>("GET", "/conversations", undefined, { signal }).then(items),
  });

export const useConversations = () => useQuery(conversationsQuery());

/** Marks a room read up to `seq` (the newest message when absent) for this user (DM-07). */
export function useMarkRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ roomId, seq }: { roomId: string; seq?: number }) =>
      api<ReadState>(
        "PUT",
        `/rooms/${roomId}/read`,
        seq === undefined ? {} : ({ seq } satisfies MarkReadRequest),
      ),
    onSuccess: (state) =>
      patchConversation(qc, state.room_id, (c) =>
        state.unread < c.unread ? { ...c, unread: state.unread } : c,
      ),
  });
}
