import type { QueryClient } from "@tanstack/react-query";
import type { MessagePages, PeoplePages } from "@/shared/api/cache-types";
import { queryKeys } from "@/shared/api/query-keys";
import type {
  Connector,
  Contacts,
  Conversation,
  IntelligenceProfile,
  IntelligenceRef,
  Me,
  Message,
  Room,
  RoomMember,
} from "@/shared/api/types.gen";
import { withIntelligence } from "../lib/contacts";

/** Applies `fn` to every cached message; a page or timeline it changes nothing in stays as it is. */
function mapCachedMessages(qc: QueryClient, fn: (roomId: string, m: Message) => Message) {
  for (const [key, data] of qc.getQueriesData<MessagePages>({ queryKey: ["rooms"] })) {
    if (key.length !== 3 || key[2] !== "messages" || !data) continue;
    const roomId = String(key[1]);
    const pages = data.pages.map((page) => {
      const items = page.items.map((m) => fn(roomId, m));
      return items.some((m, i) => m !== page.items[i]) ? { ...page, items } : page;
    });
    if (pages.some((page, i) => page !== data.pages[i])) {
      qc.setQueryData<MessagePages>(key, { ...data, pages });
    }
  }
}

/**
 * Shows an intelligence's new summary (name, handle, avatar) in every cached place it appears:
 * the sidebar, timelines, member lists, DM headers, the profile page and the account's own data
 * (PROF-03). The live `intelligence.changed` event and the profile mutations both call it.
 */
export function patchIntelligence(qc: QueryClient, ref: IntelligenceRef) {
  qc.setQueryData<IntelligenceProfile>(queryKeys.intelligence(ref.id), (old) =>
    old
      ? {
          ...old,
          kind: ref.kind,
          role: ref.role,
          display_name: ref.display_name,
          handle: ref.handle,
          avatar_url: ref.avatar_url,
          platform: ref.platform,
        }
      : old,
  );
  qc.setQueryData<Me>(queryKeys.me, (old) =>
    old?.intelligence?.id === ref.id
      ? { ...old, display_name: ref.display_name, intelligence: ref }
      : old,
  );
  // A DM is titled by its counterpart. Group previews and member names hold the old name too:
  // the server's list is the source for them.
  qc.setQueryData<Conversation[]>(queryKeys.conversations, (old) =>
    old?.map((c) =>
      c.counterpart?.id === ref.id ? { ...c, title: ref.display_name, counterpart: ref } : c,
    ),
  );
  qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });

  // A name an owner set for an AI in a room (GRP-04) keeps showing there: the message carries
  // the shown name, so the room's members tell which rows must keep it.
  mapCachedMessages(qc, (roomId, m) => {
    if (m.author.id !== ref.id) return m;
    const members = qc.getQueryData<RoomMember[]>(queryKeys.roomMembers(roomId));
    const inRoomName = members?.some((mb) => mb.intelligence?.id === ref.id && mb.display_name);
    return inRoomName
      ? { ...m, author: { ...ref, display_name: m.author.display_name } }
      : { ...m, author_name: ref.display_name, author: ref };
  });

  for (const [key, data] of qc.getQueriesData<RoomMember[] | Room>({ queryKey: ["rooms"] })) {
    if (key.length === 3 && key[2] === "members" && Array.isArray(data)) {
      if (!data.some((mb) => mb.intelligence?.id === ref.id)) continue;
      qc.setQueryData<RoomMember[]>(
        key,
        data.map((mb) =>
          mb.intelligence?.id === ref.id
            ? { ...mb, name: ref.display_name, intelligence: ref }
            : mb,
        ),
      );
    } else if (key.length === 2 && data && !Array.isArray(data)) {
      qc.setQueryData<Room>(key, withParticipant(data, ref));
    }
  }
  qc.setQueryData<Room[]>(queryKeys.rooms, (old) => old?.map((r) => withParticipant(r, ref)));

  // The contacts page, the pickers and the connectors that name this intelligence as operator.
  qc.setQueryData<Contacts>(queryKeys.contacts, (old) => old && withIntelligence(old, ref));
  for (const [key, data] of qc.getQueriesData<PeoplePages>({ queryKey: ["contacts", "people"] })) {
    if (!data?.pages.some((page) => page.items.some((p) => p.id === ref.id))) continue;
    qc.setQueryData<PeoplePages>(key, {
      ...data,
      pages: data.pages.map((page) => ({
        ...page,
        items: page.items.map((p) => (p.id === ref.id ? ref : p)),
      })),
    });
  }
  patchOperator(qc, ref);
}

/** Shows the operator's new summary on the cached connectors that name it (ACC-01). */
function patchOperator(qc: QueryClient, ref: IntelligenceRef) {
  qc.setQueryData<Connector[]>(queryKeys.connectors, (old) =>
    old?.some((c) => c.operator?.id === ref.id)
      ? old.map((c) => (c.operator?.id === ref.id ? { ...c, operator: ref } : c))
      : old,
  );
  for (const [key, c] of qc.getQueriesData<Connector>({ queryKey: queryKeys.connectors })) {
    if (key.length === 2 && c?.operator?.id === ref.id) {
      qc.setQueryData<Connector>(key, { ...c, operator: ref });
    }
  }
}

/** The room with `ref` in place of the DM participant that has its id. */
function withParticipant(room: Room, ref: IntelligenceRef): Room {
  if (!room.participants?.some((p) => p.id === ref.id)) return room;
  return { ...room, participants: room.participants.map((p) => (p.id === ref.id ? ref : p)) };
}
