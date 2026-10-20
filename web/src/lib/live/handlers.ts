import type { QueryClient } from "@tanstack/react-query";
import { fetchMessages, type MessagePages, queryKeys } from "@/lib/api/queries";
import type {
  Agent,
  Connector,
  LiveEvent,
  Message,
  OutboxUpdate,
  Room,
  RoomMember,
} from "@/lib/api/types.gen";
import type { LiveState } from "./store";

/*
 * Live event → cache patch. Each handler is a pure function of (query client, store actions,
 * event): no network, no React. Patches only touch data already in the cache, so events about
 * rooms the user never opened are ignored (UI-04 edge case).
 */

export type LiveActions = Pick<
  LiveState,
  "setPresence" | "setConnectorState" | "setLastLinkedIdentity"
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

const RESOURCE_KEYS: Record<string, "rooms" | "agents" | "connectors"> = {
  room: "rooms",
  agent: "agents",
  connector: "connectors",
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
      return;
    }

    case "room.changed": {
      const room = event.data;
      qc.setQueryData<Room>(queryKeys.room(room.id), room);
      qc.setQueryData<Room[]>(queryKeys.rooms, (old) => {
        if (!old) return old;
        return old.some((r) => r.id === room.id)
          ? old.map((r) => (r.id === room.id ? room : r))
          : [...old, room];
      });
      return;
    }

    case "room.deleted": {
      const id = event.data.room_id;
      qc.removeQueries({ queryKey: queryKeys.room(id) });
      qc.setQueryData<Room[]>(queryKeys.rooms, (old) => old?.filter((r) => r.id !== id));
      return;
    }

    case "room.members":
      qc.invalidateQueries({ queryKey: queryKeys.roomMembers(event.data.room_id) });
      return;

    case "access.changed": {
      const kind = RESOURCE_KEYS[event.data.resource_kind];
      if (!kind) return;
      qc.invalidateQueries({ queryKey: [kind], exact: true });
      qc.invalidateQueries({ queryKey: [kind, event.data.resource_id] });
      if (kind !== "rooms") qc.invalidateQueries({ queryKey: queryKeys.chats });
      return;
    }

    case "agent.presence": {
      const { agent_id, online, last_seen_at } = event.data;
      actions.setPresence(agent_id, { online, last_seen_at });
      patchList<Agent>(qc, queryKeys.agents, agent_id, (a) => ({
        ...a,
        online,
        last_seen_at: last_seen_at ?? a.last_seen_at,
      }));
      // Agents are created on their first connection: an unknown id means the list is stale.
      const cached = qc.getQueryData<Agent[]>(queryKeys.agents);
      if (cached && !cached.some((a) => a.id === agent_id)) {
        qc.invalidateQueries({ queryKey: queryKeys.agents, exact: true });
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
  qc.invalidateQueries({ queryKey: queryKeys.agents });
  qc.invalidateQueries({ queryKey: queryKeys.connectors });
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
