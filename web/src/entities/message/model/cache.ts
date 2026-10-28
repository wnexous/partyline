import type { QueryClient } from "@tanstack/react-query";
import { patchConversation, previewOf } from "@/entities/room/@x/message";
import type { FilePages, MessagePages } from "@/shared/api/cache-types";
import { queryKeys } from "@/shared/api/query-keys";
import type { ActivityItem, Message, Reaction } from "@/shared/api/types.gen";
import { findMessage, replaceMessage } from "../lib/pages";

/**
 * A message was edited or deleted, by this client (the answer of the request) or by anyone (the live
 * room.message_updated frame): everywhere the cache shows it, it becomes the message as it is now (EDT-06,
 * DLT-06). The timeline, the preview of the conversation if it is the last message, the dashboard feed; the
 * files of a deleted message leave the files lists. Nothing is added, counted as unread or moved up: the message
 * keeps its place. Applying the same update twice changes nothing more.
 */
export function applyMessageUpdate(qc: QueryClient, m: Message) {
  qc.setQueryData<MessagePages>(queryKeys.roomMessages(m.room_id), (old) => replaceMessage(old, m));
  patchConversation(qc, m.room_id, (c) =>
    c.last_message?.seq === m.seq ? { ...c, last_message: previewOf(m, c.kind) } : c,
  );
  qc.setQueryData<ActivityItem[]>(queryKeys.activity, (old) =>
    old?.map((a) => (a.message.id === m.id ? { ...a, message: m } : a)),
  );
  if (m.deleted_at === undefined) return;
  for (const [key, data] of qc.getQueriesData<FilePages>({
    queryKey: ["rooms", m.room_id, "files"],
  })) {
    if (!data) continue;
    qc.setQueryData<FilePages>(key, {
      ...data,
      pages: data.pages.map((page) => ({
        ...page,
        items: page.items.filter((f) => f.message_id !== m.id),
      })),
    });
  }
}

/** The reactions the timeline holds for a message; none when it does not hold the message. */
export const readReactions = (
  qc: QueryClient,
  roomId: string,
  messageId: string,
): Reaction[] | undefined =>
  findMessage(qc.getQueryData<MessagePages>(queryKeys.roomMessages(roomId)), messageId)?.reactions;

/**
 * The reactions of a message changed, by this client (a toggle, and the answer of the request) or by anyone (the
 * live room.reactions frame): everywhere the cache shows the message, they become the list as it is now (RCT-06).
 * The message keeps its place and is not counted as unread; a deleted message has none (RCT-07), so a frame that
 * outran its delete finds it unchanged. Applying the same list twice changes nothing more.
 */
export function applyReactions(
  qc: QueryClient,
  roomId: string,
  messageId: string,
  reactions: Reaction[],
) {
  qc.setQueryData<MessagePages>(queryKeys.roomMessages(roomId), (old) => {
    const m = findMessage(old, messageId);
    return m && m.deleted_at === undefined ? replaceMessage(old, { ...m, reactions }) : old;
  });
  qc.setQueryData<ActivityItem[]>(queryKeys.activity, (old) =>
    old?.map((a) =>
      a.message.id === messageId && a.message.deleted_at === undefined
        ? { ...a, message: { ...a.message, reactions } }
        : a,
    ),
  );
}
