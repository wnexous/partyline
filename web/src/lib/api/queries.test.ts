import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Conversation } from "@/lib/api/types.gen";
import { makeConversation, makeMessage } from "@/test/fixtures";
import { markConversationRead, olderPageCursor, queryKeys, roomMessagesQuery } from "./queries";

describe("room messages infinite query", () => {
  const fetchMock = vi.fn<typeof fetch>();

  beforeEach(() => vi.stubGlobal("fetch", fetchMock));
  afterEach(() => {
    fetchMock.mockReset();
    vi.unstubAllGlobals();
  });

  it("loads the newest page, then older pages with before_seq, each oldest first", async () => {
    const page = (seqs: number[], hasMore: boolean) =>
      new Response(
        JSON.stringify({ items: seqs.map((seq) => makeMessage({ seq })), has_more: hasMore }),
        { status: 200 },
      );
    // The server may answer newest first; the cache must hold each page oldest first.
    fetchMock.mockResolvedValueOnce(page([60, 59, 58], true));
    fetchMock.mockResolvedValueOnce(page([57, 56], false));

    const qc = new QueryClient();
    await qc.fetchInfiniteQuery({ ...roomMessagesQuery("r1"), pages: 2 });

    expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/v1/rooms/r1/messages?limit=50");
    expect(fetchMock.mock.calls.at(-1)?.[0]).toBe(
      "/api/v1/rooms/r1/messages?before_seq=58&limit=50",
    );
    const data = qc.getQueryData(queryKeys.roomMessages("r1")) as {
      pages: { items: { seq: number }[] }[];
    };
    expect(data.pages.map((p) => p.items.map((m) => m.seq))).toEqual([
      [58, 59, 60],
      [56, 57],
    ]);
  });

  it("has no older cursor when has_more is false", () => {
    expect(olderPageCursor({ items: [makeMessage({ seq: 3 })], has_more: false })).toBeUndefined();
    expect(olderPageCursor({ items: [], has_more: true })).toBeUndefined();
  });
});

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
