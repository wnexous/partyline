import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { makeMessage } from "@/test/fixtures";
import { olderPageCursor, queryKeys, roomMessagesQuery } from "./queries";

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
