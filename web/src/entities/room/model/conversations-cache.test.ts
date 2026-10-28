import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { queryKeys } from "@/shared/api/query-keys";
import type { Conversation } from "@/shared/api/types.gen";
import { makeConversation } from "@/test/fixtures";
import { markConversationRead } from "./conversations-cache";

describe("markConversationRead (DM-07, LAY-02)", () => {
  /** A cached list with one conversation: `unread` messages unread, its last message at `lastSeq`. */
  function seed(unread: number, lastSeq: number) {
    const qc = new QueryClient();
    qc.setQueryData<Conversation[]>(queryKeys.conversations, [
      makeConversation({
        id: "r1",
        unread,
        last_message: {
          seq: lastSeq,
          author_id: "u2",
          author_name: "Bob Lima",
          text: "last",
          created_at: "2026-10-19T12:00:00Z",
        },
      }),
    ]);
    return qc;
  }
  const unreadOf = (qc: QueryClient) =>
    qc.getQueryData<Conversation[]>(queryKeys.conversations)?.[0]?.unread;

  it("never raises the count: five messages after the read position, one unread, stays one (W1)", () => {
    const qc = seed(1, 10);
    markConversationRead(qc, "r1", 5);
    expect(unreadOf(qc)).toBe(1);
  });

  it("lowers the count to the messages after the read position", () => {
    const qc = seed(3, 10);
    markConversationRead(qc, "r1", 9);
    expect(unreadOf(qc)).toBe(1);
  });

  it("clears the count when the read position is the last message", () => {
    const qc = seed(2, 10);
    markConversationRead(qc, "r1", 10);
    expect(unreadOf(qc)).toBe(0);
  });
});
