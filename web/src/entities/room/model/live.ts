import type { QueryClient } from "@tanstack/react-query";
import { patchList } from "@/shared/api/patch-list";
import { queryKeys } from "@/shared/api/query-keys";
import type { ActivityItem, Conversation, Me, Message, Room } from "@/shared/api/types.gen";
import { newestActivityFirst } from "../lib/order";
import { previewOf } from "../lib/preview";
import { patchConversation } from "./conversations-cache";

/*
 * What the live frames do to the room and conversation caches. Pure cache patches.
 */

/** Rooms whose live messages already caused a refetch of a list that did not have them. */
const askedFor = new WeakMap<QueryClient, Set<string>>();

/**
 * Fetches the conversation list again because `roomId` is not in it. `once` is for messages: a
 * room the list still lacks after one refetch (a DM an AI's owner only views gets live messages
 * but is never listed) must not cost a request per message. Room changes ask every time.
 */
function refetchConversationsFor(qc: QueryClient, roomId: string, once: boolean) {
  let rooms = askedFor.get(qc);
  if (!rooms) {
    rooms = new Set();
    askedFor.set(qc, rooms);
  }
  if (once && rooms.has(roomId)) return;
  rooms.add(roomId);
  qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });
}

/** A live message raises the room's sequence number, in the room cache and the rooms list. */
export function noteMessageInRoom(qc: QueryClient, m: Message) {
  const bump = (room: Room) =>
    m.seq > room.last_seq ? { ...room, last_seq: m.seq, updated_at: m.created_at } : room;
  qc.setQueryData<Room>(queryKeys.room(m.room_id), (old) => (old ? bump(old) : old));
  patchList<Room>(qc, queryKeys.rooms, m.room_id, bump);
}

/**
 * Moves the message's conversation to the top with its preview, and counts it as unread unless
 * the user is looking at that conversation or wrote the message (LAY-02, DM-06).
 */
export function bumpConversation(qc: QueryClient, openRoomId: string | null, m: Message) {
  const list = qc.getQueryData<Conversation[]>(queryKeys.conversations);
  if (!list) return; // the sidebar never loaded it: its first fetch has the message
  const conversation = list.find((c) => c.id === m.room_id);
  if (!conversation) {
    refetchConversationsFor(qc, m.room_id, true);
    return;
  }
  // A message delivered twice, or older than the preview, is already counted.
  if (conversation.last_message && m.seq <= conversation.last_message.seq) return;
  const meId = qc.getQueryData<Me>(queryKeys.me)?.intelligence?.id;
  const mine = m.author.id !== "" && m.author.id === meId;
  const unread = conversation.unread + (mine || openRoomId === m.room_id ? 0 : 1);
  patchConversation(qc, m.room_id, (c) => ({
    ...c,
    unread,
    activity_at: m.created_at,
    last_message: previewOf(m, c.kind),
  }));
  qc.setQueryData<Conversation[]>(queryKeys.conversations, (old) =>
    old ? [...old].sort(newestActivityFirst) : old,
  );
}

/** A room changed (room.changed): the room, the rooms list, the feed's name and the sidebar row. */
export function changeRoom(qc: QueryClient, room: Room) {
  qc.setQueryData<Room>(queryKeys.room(room.id), room);
  qc.setQueryData<Room[]>(queryKeys.rooms, (old) => {
    if (!old) return old;
    return old.some((r) => r.id === room.id)
      ? old.map((r) => (r.id === room.id ? room : r))
      : [...old, room];
  });
  qc.setQueryData<ActivityItem[]>(queryKeys.activity, (old) =>
    old?.map((a) => (a.room.id === room.id ? { ...a, room: { id: room.id, name: room.name } } : a)),
  );
  // A group is titled by its name; a DM keeps the other participant's name.
  const listed = qc.getQueryData<Conversation[]>(queryKeys.conversations);
  if (listed && !listed.some((c) => c.id === room.id)) {
    refetchConversationsFor(qc, room.id, false);
  } else {
    patchConversation(qc, room.id, (c) => ({
      ...c,
      title: c.kind === "group" ? room.name : c.title,
      // A DM is drawn with its counterpart; only a group has a photo of its own (GAV-02).
      avatar_url: c.kind === "group" ? room.avatar_url : c.avatar_url,
      my_level: room.my_level,
      org_ids: room.org_ids,
    }));
  }
}

/** A room was deleted (room.deleted): it leaves every cached list at once (ROOMDEL-02). */
export function removeRoom(qc: QueryClient, id: string) {
  qc.removeQueries({ queryKey: queryKeys.room(id) });
  qc.setQueryData<Room[]>(queryKeys.rooms, (old) => old?.filter((r) => r.id !== id));
  qc.setQueryData<ActivityItem[]>(queryKeys.activity, (old) =>
    old?.filter((a) => a.room.id !== id),
  );
  qc.setQueryData<Conversation[]>(queryKeys.conversations, (old) =>
    old?.filter((c) => c.id !== id),
  );
}
