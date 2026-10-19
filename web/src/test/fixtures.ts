import type { Message } from "@/lib/api/types.gen";

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
