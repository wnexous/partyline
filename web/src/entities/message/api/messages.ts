import {
  infiniteQueryOptions,
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from "@tanstack/react-query";
import { api, isApiError } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import {
  DefaultPageSize,
  type EditMessageRequest,
  type Message,
  MultipartFileField,
  MultipartMessageField,
  type Page,
  type PostMessageRequest,
  type Upload,
} from "@/shared/api/types.gen";
import { fitsOneRequest, type UploadProgress, uploadFile } from "@/shared/api/uploads";
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
 * each page are oldest first. Live events append to pages[0] (app/live/dispatch.ts).
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

/**
 * How the files of a post that go through upload sessions report and stop (LF-01). A file is
 * known by its File object, the one in `files`.
 */
export interface FileTransfer {
  /** Aborting it stops the uploads, and the post rejects with an AbortError; the post itself, once every file is up, is not stopped. */
  signal?: AbortSignal;
  /** The files go up through sessions now; `onEnd` follows when that is over, however it ended. */
  onStart?: () => void;
  onEnd?: () => void;
  onProgress?: (file: File, progress: UploadProgress) => void;
  /** The upload session a file got (to resume it if this post does not go through). */
  onSession?: (file: File, upload: Upload) => void;
  /** The session an earlier try of this file started, to go on from instead of starting over. */
  sessionOf?: (file: File) => string | undefined;
}

export interface PostMessageInput extends PostMessageRequest {
  files?: File[];
  transfer?: FileTransfer;
}

/**
 * Posts a message. Files that weigh little together go in one multipart request with it, as they
 * always did; above that (or when the server refuses the multipart body as too large) every file
 * goes up first through an upload session, one after the other with its progress, and the message
 * attaches them by `upload_ids`, in the order they were added (LF-01).
 *
 * `onSent` runs when the post went through, also when the component that sent it is gone by then
 * (the callbacks of `mutate` are not run for an unmounted observer).
 */
export function usePostMessage(roomId: string, onSent?: (input: PostMessageInput) => void) {
  return useMutation({
    mutationKey: postMessageKey(roomId),
    onSuccess: (_message, input) => onSent?.(input),
    mutationFn: async ({ files, transfer, ...req }: PostMessageInput) => {
      const url = `/rooms/${roomId}/messages`;
      if (!files?.length) return api<Message>("POST", url, req);
      if (fitsOneRequest(files)) {
        const form = new FormData();
        form.append(MultipartMessageField, JSON.stringify(req));
        for (const file of files) form.append(MultipartFileField, file, file.name);
        try {
          return await api<Message>("POST", url, form);
        } catch (err) {
          // A server (or a proxy before it) that takes less in one request than this client
          // guessed: the same files go through sessions instead.
          if (!(isApiError(err) && err.status === 413)) throw err;
        }
      }
      transfer?.onStart?.();
      try {
        const ids: string[] = [];
        for (const file of files) {
          const upload = await uploadFile(file, {
            signal: transfer?.signal,
            resume: transfer?.sessionOf?.(file),
            onSession: (u) => transfer?.onSession?.(file, u),
            onProgress: (p) => transfer?.onProgress?.(file, p),
          });
          ids.push(upload.id);
        }
        // Not cancelled any more once the files are up: a post that reached the server stays.
        const upload_ids = [...(req.upload_ids ?? []), ...ids];
        return await api<Message>("POST", url, { ...req, upload_ids });
      } finally {
        transfer?.onEnd?.();
      }
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
