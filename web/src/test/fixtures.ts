import type { Me, Message, Room } from "@/lib/api/types.gen";

/** Synthetic fixtures; never real names, numbers or chat ids. */
export function makeMessage(over: Partial<Message> = {}): Message {
  const seq = over.seq ?? 1;
  return {
    id: `m-${over.room_id ?? "r1"}-${seq}`,
    room_id: "r1",
    seq,
    author_kind: "user",
    author_id: "u1",
    author_name: "Ana",
    kind: "text",
    text: `message ${seq}`,
    attachments: [],
    created_at: "2026-10-19T12:00:00Z",
    ...over,
  };
}

export function makeMe(over: Partial<Me> = {}): Me {
  return {
    id: "u1",
    username: "ana",
    display_name: "Ana Souza",
    role: "member",
    locale: "en",
    created_at: "2026-10-01T12:00:00Z",
    ...over,
  };
}

export function makeRoom(over: Partial<Room> = {}): Room {
  return {
    id: "r1",
    name: "general",
    description: "",
    owner: { id: "u1", username: "ana", display_name: "Ana Souza" },
    visibility: "private",
    my_level: "manage",
    bridge_chats: false,
    retention_days: 90,
    agents_paused: false,
    last_seq: 0,
    created_at: "2026-10-01T12:00:00Z",
    updated_at: "2026-10-19T12:00:00Z",
    ...over,
  };
}
