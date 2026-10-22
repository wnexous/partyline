import type { QueryClient } from "@tanstack/react-query";
import {
  type FileFilter,
  type FilePages,
  fetchMessages,
  type MessagePages,
  markConversationRead,
  messageFiles,
  newestActivityFirst,
  patchConversation,
  patchIntelligence,
  previewOf,
  queryKeys,
  trimActivity,
} from "@/lib/api/queries";
import type {
  ActivityItem,
  Agent,
  Connector,
  Conversation,
  LiveEvent,
  Me,
  Message,
  OutboxUpdate,
  Room,
  RoomMember,
  RoomRef,
} from "@/lib/api/types.gen";
import type { LiveState } from "./store";

/*
 * Live event → cache patch. Each handler is a pure function of (query client, store actions,
 * event): no network, no React. Patches only touch data already in the cache, so events about
 * rooms the user never opened are ignored (UI-04 edge case).
 */

export type LiveActions = Pick<
  LiveState,
  "setPresence" | "setConnectorState" | "setLastLinkedIdentity" | "openRoomId"
>;

/** Inserts messages into the newest page, keeping seq order and dropping known ids/seqs. */
export function mergeMessages(data: MessagePages | undefined, incoming: Message[]) {
  if (!data || data.pages.length === 0) return data;
  const known = new Set<string>();
  const knownSeq = new Set<number>();
  for (const page of data.pages) {
    for (const m of page.items) {
      known.add(m.id);
      knownSeq.add(m.seq);
    }
  }
  const fresh = incoming.filter((m) => !known.has(m.id) && !knownSeq.has(m.seq));
  if (fresh.length === 0) return data;
  const [newest, ...older] = data.pages;
  if (!newest) return data;
  const items = [...newest.items, ...fresh].sort((a, b) => a.seq - b.seq);
  return { ...data, pages: [{ ...newest, items }, ...older] };
}

/** Highest sequence number cached for a room, or undefined when its timeline is not cached. */
export function lastSeq(qc: QueryClient, roomId: string): number | undefined {
  const data = qc.getQueryData<MessagePages>(queryKeys.roomMessages(roomId));
  if (!data) return undefined;
  let max: number | undefined;
  for (const page of data.pages) {
    for (const m of page.items) if (max === undefined || m.seq > max) max = m.seq;
  }
  return max;
}

function patchList<T extends { id: string }>(
  qc: QueryClient,
  key: readonly unknown[],
  id: string,
  patch: (item: T) => T,
) {
  qc.setQueryData<T[]>(key, (old) => old?.map((item) => (item.id === id ? patch(item) : item)));
}

function patchOutbox(message: Message, update: OutboxUpdate): Message {
  const { room_id: _room, message_id: _message, ...item } = update;
  const outbox = message.outbox ?? [];
  const exists = outbox.some((o) => o.id === item.id);
  return {
    ...message,
    outbox: exists ? outbox.map((o) => (o.id === item.id ? item : o)) : [...outbox, item],
  };
}

/** The room's name as the cache knows it: the feed itself, the room, or the rooms list. */
function roomRef(qc: QueryClient, feed: ActivityItem[], roomId: string): RoomRef | undefined {
  const known = feed.find((a) => a.room.id === roomId)?.room;
  if (known) return known;
  const room =
    qc.getQueryData<Room>(queryKeys.room(roomId)) ??
    qc.getQueryData<Room[]>(queryKeys.rooms)?.find((r) => r.id === roomId);
  return room ? { id: room.id, name: room.name } : undefined;
}

/** Puts a live message on the dashboard feed, when the feed is cached. */
function prependActivity(qc: QueryClient, m: Message) {
  const feed = qc.getQueryData<ActivityItem[]>(queryKeys.activity);
  if (!feed || feed.some((a) => a.message.id === m.id)) return;
  const room = roomRef(qc, feed, m.room_id);
  if (!room) {
    // A room the cache cannot name (new or newly shared): the server's feed has it.
    qc.invalidateQueries({ queryKey: queryKeys.activity });
    return;
  }
  qc.setQueryData<ActivityItem[]>(
    queryKeys.activity,
    trimActivity([{ room, message: m }, ...feed]),
  );
}

/** Puts a live message's files at the top of the room's cached files lists (FILES-05). */
function prependFiles(qc: QueryClient, m: Message) {
  if (m.attachments.length === 0) return;
  const files = messageFiles(m);
  for (const [key, data] of qc.getQueriesData<FilePages>({
    queryKey: ["rooms", m.room_id, "files"],
  })) {
    const first = data?.pages[0];
    if (!data || !first) continue;
    const kind = key[3] as FileFilter;
    const known = new Set(data.pages.flatMap((p) => p.items.map((f) => f.id)));
    const fresh = files.filter((f) => !known.has(f.id) && (kind === "all" || f.kind === kind));
    if (fresh.length === 0) continue;
    qc.setQueryData<FilePages>(key, {
      ...data,
      pages: [{ ...first, items: [...fresh, ...first.items] }, ...data.pages.slice(1)],
    });
  }
}

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

/**
 * Moves the message's conversation to the top with its preview, and counts it as unread unless
 * the user is looking at that conversation or wrote the message (LAY-02, DM-06).
 */
function bumpConversation(qc: QueryClient, openRoomId: string | null, m: Message) {
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

const RESOURCE_KEYS: Record<string, "rooms" | "agents" | "connectors" | "projects"> = {
  room: "rooms",
  agent: "agents",
  connector: "connectors",
  project: "projects",
};

export function handleLiveEvent(qc: QueryClient, actions: LiveActions, event: LiveEvent): void {
  switch (event.type) {
    case "hello":
      return;

    case "room.message": {
      const m = event.data;
      qc.setQueryData<MessagePages>(queryKeys.roomMessages(m.room_id), (old) =>
        mergeMessages(old, [m]),
      );
      const bump = (room: Room) =>
        m.seq > room.last_seq ? { ...room, last_seq: m.seq, updated_at: m.created_at } : room;
      qc.setQueryData<Room>(queryKeys.room(m.room_id), (old) => (old ? bump(old) : old));
      patchList<Room>(qc, queryKeys.rooms, m.room_id, bump);
      bumpConversation(qc, actions.openRoomId, m);
      prependActivity(qc, m);
      prependFiles(qc, m);
      return;
    }

    case "room.read":
      markConversationRead(qc, event.data.room_id, event.data.seq);
      return;

    case "room.changed": {
      const room = event.data;
      qc.setQueryData<Room>(queryKeys.room(room.id), room);
      qc.setQueryData<Room[]>(queryKeys.rooms, (old) => {
        if (!old) return old;
        return old.some((r) => r.id === room.id)
          ? old.map((r) => (r.id === room.id ? room : r))
          : [...old, room];
      });
      qc.setQueryData<ActivityItem[]>(queryKeys.activity, (old) =>
        old?.map((a) =>
          a.room.id === room.id ? { ...a, room: { id: room.id, name: room.name } } : a,
        ),
      );
      // A group is titled by its name; a DM keeps the other participant's name.
      const listed = qc.getQueryData<Conversation[]>(queryKeys.conversations);
      if (listed && !listed.some((c) => c.id === room.id)) {
        refetchConversationsFor(qc, room.id, false);
      } else {
        patchConversation(qc, room.id, (c) => ({
          ...c,
          title: c.kind === "group" ? room.name : c.title,
          my_level: room.my_level,
        }));
      }
      return;
    }

    case "room.deleted": {
      const id = event.data.room_id;
      qc.removeQueries({ queryKey: queryKeys.room(id) });
      qc.setQueryData<Room[]>(queryKeys.rooms, (old) => old?.filter((r) => r.id !== id));
      qc.setQueryData<ActivityItem[]>(queryKeys.activity, (old) =>
        old?.filter((a) => a.room.id !== id),
      );
      qc.setQueryData<Conversation[]>(queryKeys.conversations, (old) =>
        old?.filter((c) => c.id !== id),
      );
      return;
    }

    case "room.members":
      qc.invalidateQueries({ queryKey: queryKeys.roomMembers(event.data.room_id) });
      // The list's member names feed the sidebar search.
      qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });
      return;

    case "access.changed": {
      const kind = RESOURCE_KEYS[event.data.resource_kind];
      if (!kind) return;
      qc.invalidateQueries({ queryKey: [kind], exact: true });
      qc.invalidateQueries({ queryKey: [kind, event.data.resource_id] });
      if (kind === "agents" || kind === "connectors") {
        qc.invalidateQueries({ queryKey: queryKeys.chats });
      }
      // Sharing, membership and DM access decide which conversations the user has.
      if (kind === "rooms")
        qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });
      return;
    }

    case "agent.presence": {
      const { agent_id, online, last_seen_at } = event.data;
      actions.setPresence(agent_id, { online, last_seen_at });
      for (const key of [queryKeys.agents, queryKeys.agentsWithArchived]) {
        patchList<Agent>(qc, key, agent_id, (a) => ({
          ...a,
          online,
          // A connect unarchives the agent on the server (PROJ-02).
          archived: online ? false : a.archived,
          last_seen_at: last_seen_at ?? a.last_seen_at,
        }));
        // Agents are created on their first connection: an unknown id means the list is stale.
        const cached = qc.getQueryData<Agent[]>(key);
        if (cached && !cached.some((a) => a.id === agent_id)) {
          qc.invalidateQueries({ queryKey: key, exact: true });
        }
      }
      for (const [key, members] of qc.getQueriesData<RoomMember[]>({ queryKey: ["rooms"] })) {
        if (key[2] !== "members" || !members) continue;
        if (!members.some((mb) => mb.kind === "agent" && mb.member_id === agent_id)) continue;
        qc.setQueryData<RoomMember[]>(
          key,
          members.map((mb) =>
            mb.kind === "agent" && mb.member_id === agent_id ? { ...mb, online } : mb,
          ),
        );
      }
      return;
    }

    case "connector.status": {
      const { connector_id, status, last_error } = event.data;
      actions.setConnectorState(connector_id, { status, last_error });
      const patch = (c: Connector): Connector => ({ ...c, status, last_error });
      patchList<Connector>(qc, queryKeys.connectors, connector_id, patch);
      qc.setQueryData<Connector>(queryKeys.connector(connector_id), (old) =>
        old ? patch(old) : old,
      );
      return;
    }

    case "chat.discovered":
      qc.invalidateQueries({ queryKey: queryKeys.connectorChats(event.data.connector_id) });
      qc.invalidateQueries({ queryKey: queryKeys.chats });
      return;

    case "outbox.status": {
      const update = event.data;
      qc.setQueryData<MessagePages>(queryKeys.roomMessages(update.room_id), (old) => {
        if (!old) return old;
        return {
          ...old,
          pages: old.pages.map((page) =>
            page.items.some((m) => m.id === update.message_id)
              ? {
                  ...page,
                  items: page.items.map((m) =>
                    m.id === update.message_id ? patchOutbox(m, update) : m,
                  ),
                }
              : page,
          ),
        };
      });
      return;
    }

    case "identity.linked":
      actions.setLastLinkedIdentity(event.data);
      qc.invalidateQueries({ queryKey: queryKeys.identities });
      return;

    case "intelligence.changed":
      patchIntelligence(qc, event.data);
      return;

    case "lagged":
      // The server dropped events for us: everything visible may be stale.
      qc.invalidateQueries();
      return;
  }
}

/** Room ids whose timelines are cached. */
function cachedTimelines(qc: QueryClient): { roomId: string; active: boolean }[] {
  return qc
    .getQueryCache()
    .findAll({ queryKey: ["rooms"] })
    .filter((q) => q.queryKey.length === 3 && q.queryKey[2] === "messages")
    .map((q) => ({ roomId: String(q.queryKey[1]), active: q.isActive() }));
}

/**
 * After a reconnect (UI-06): refetch the rooms list, fetch each open timeline after its last
 * seen sequence, and mark closed timelines stale so they reload when reopened.
 */
export async function catchUp(qc: QueryClient): Promise<void> {
  qc.invalidateQueries({ queryKey: queryKeys.rooms, exact: true });
  qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });
  qc.invalidateQueries({ queryKey: queryKeys.agents });
  qc.invalidateQueries({ queryKey: queryKeys.connectors });
  qc.invalidateQueries({ queryKey: queryKeys.activity });
  // Files lists refetch: messages missed while offline may have carried files.
  qc.invalidateQueries({
    predicate: (q) => q.queryKey[0] === "rooms" && q.queryKey[2] === "files",
  });
  await Promise.all(
    cachedTimelines(qc).map(async ({ roomId, active }) => {
      const after = lastSeq(qc, roomId);
      if (!active || after === undefined) {
        qc.invalidateQueries({ queryKey: queryKeys.roomMessages(roomId) });
        return;
      }
      try {
        let cursor = after;
        for (;;) {
          const page = await fetchMessages(roomId, { after_seq: cursor });
          qc.setQueryData<MessagePages>(queryKeys.roomMessages(roomId), (old) =>
            mergeMessages(old, page.items),
          );
          const last = page.items.at(-1);
          if (!page.has_more || !last) break;
          cursor = last.seq;
        }
      } catch {
        qc.invalidateQueries({ queryKey: queryKeys.roomMessages(roomId) });
      }
    }),
  );
}
