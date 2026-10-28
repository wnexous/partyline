import {
  infiniteQueryOptions,
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import {
  DefaultPageSize,
  type EditMessageRequest,
  type Message,
  MultipartFileField,
  MultipartMessageField,
  type Page,
  type PostMessageRequest,
} from "@/shared/api/types.gen";
import { newestSeqOf, normalizePage, olderPageCursor } from "../lib/pages";
import { applyMessageUpdate } from "../model/cache";

export function fetchMessages(
  roomId: string,
  cursor: { before_seq?: number; after_seq?: number },
  signal?: AbortSignal,
) {
  return api<Page<Message>>("GET", `/rooms/${roomId}/messages`, undefined, {
    query: { ...cursor, limit: DefaultPageSize },
    signal,
  }).then(normalizePage);
}

/**
 * Room timeline: pages[0] is the newest page; fetchNextPage loads older messages. Items inside
 * each page are oldest first. Live events append to pages[0] (lib/live/handlers.ts).
 */
export const roomMessagesQuery = (roomId: string) =>
  infiniteQueryOptions({
    queryKey: queryKeys.roomMessages(roomId),
    queryFn: ({ pageParam, signal }) => fetchMessages(roomId, { before_seq: pageParam }, signal),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: olderPageCursor,
    staleTime: Number.POSITIVE_INFINITY,
  });

export const useRoomMessages = (roomId: string) => useInfiniteQuery(roomMessagesQuery(roomId));

/** The newest message seq of a room's timeline; re-renders only when that number changes. */
export const useNewestSeq = (roomId: string) =>
  useInfiniteQuery({ ...roomMessagesQuery(roomId), select: newestSeqOf }).data;

/** Identifies one room's posts, so the page can read the refusal of its own composer. */
export const postMessageKey = (roomId: string) => ["post-message", roomId] as const;

export interface PostMessageInput extends PostMessageRequest {
  files?: File[];
}

/**
 * Posts a message. `onSent` runs when the post went through, also when the component that sent it
 * is gone by then (the callbacks of `mutate` are not run for an unmounted observer).
 */
export function usePostMessage(roomId: string, onSent?: (input: PostMessageInput) => void) {
  return useMutation({
    mutationKey: postMessageKey(roomId),
    onSuccess: (_message, input) => onSent?.(input),
    mutationFn: ({ files, ...req }: PostMessageInput) => {
      if (!files?.length) return api<Message>("POST", `/rooms/${roomId}/messages`, req);
      const form = new FormData();
      form.append(MultipartMessageField, JSON.stringify(req));
      for (const file of files) form.append(MultipartFileField, file, file.name);
      return api<Message>("POST", `/rooms/${roomId}/messages`, form);
    },
    // The stored message comes back through the live room.message event; no cache write here,
    // so the timeline has a single source of appends.
  });
}

/** Replaces the text of a message the viewer wrote (EDT-01); the answer is the message as it is now. */
export function useEditMessage(roomId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, text }: { id: string; text: string }) =>
      api<Message>("PATCH", `/rooms/${roomId}/messages/${id}`, {
        text,
      } satisfies EditMessageRequest),
    // The live frame says the same to everyone else; writing the answer here keeps this client right when its
    // socket is down.
    onSuccess: (m) => applyMessageUpdate(qc, m),
  });
}

/** Deletes a message (DLT-01); the answer is its placeholder. */
export function useDeleteMessage(roomId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<Message>("DELETE", `/rooms/${roomId}/messages/${id}`),
    onSuccess: (m) => applyMessageUpdate(qc, m),
  });
}
