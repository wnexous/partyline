import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMessage } from "@/test/fixtures";
import type { MessagePages } from "./queries";
import { applyReactions, queryKeys, useToggleReaction } from "./queries";
import type { ActivityItem, Message, Reaction } from "@/shared/api/types.gen";

/*
 * What a tap on a reaction sends and leaves in the cache (RCT-01, RCT-03, RCT-08): the reaction shows at once,
 * the answer of the server settles it, and a request that fails undoes only itself.
 */

const ME = "u1";
const THEM = "u2";

let qc: QueryClient;
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);

const seed = (...messages: Message[]) =>
  qc.setQueryData<MessagePages>(queryKeys.roomMessages("r1"), {
    pages: [{ items: messages, has_more: false }],
    pageParams: [undefined],
  });
const reactionsOf = (seq: number): Reaction[] | undefined =>
  qc
    .getQueryData<MessagePages>(queryKeys.roomMessages("r1"))
    ?.pages.flatMap((p) => p.items)
    .find((m) => m.seq === seq)?.reactions;
const answer = (reactions: Reaction[]) => json(200, { items: reactions });

/** A response the test lets go of when it likes, to look at the cache while the request is out. */
function pending() {
  let release: (r: Response) => void = () => {};
  const response = new Promise<Response>((resolve) => {
    release = resolve;
  });
  return { response, release };
}

beforeEach(() => {
  qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
});
afterEach(() => {
  qc.clear();
  vi.unstubAllGlobals();
});

describe("what a toggle sends (RCT-03)", () => {
  it("adds with a POST of the emoji to the reactions of the message", async () => {
    const { calls } = mockApi({
      "POST /rooms/r1/messages/m-r1-2/reactions": answer([{ emoji: "👍", by: [ME] }]),
    });
    seed(makeMessage({ seq: 1 }), makeMessage({ seq: 2 }));
    const { result } = renderHook(() => useToggleReaction("r1"), { wrapper });
    await act(() =>
      result.current.mutateAsync({ messageId: "m-r1-2", emoji: "👍", add: true, by: ME }),
    );
    expect(calls).toHaveLength(1);
    expect(calls[0]).toMatchObject({
      method: "POST",
      path: "/rooms/r1/messages/m-r1-2/reactions",
      body: { emoji: "👍" },
    });
  });

  it("takes back with a DELETE of the emoji, percent-encoded, in the path", async () => {
    const { calls } = mockApi({
      "DELETE /rooms/r1/messages/m-r1-2/reactions/%E2%9D%A4%EF%B8%8F": answer([]),
    });
    seed(makeMessage({ seq: 2, reactions: [{ emoji: "❤️", by: [ME] }] }));
    const { result } = renderHook(() => useToggleReaction("r1"), { wrapper });
    await act(() =>
      result.current.mutateAsync({ messageId: "m-r1-2", emoji: "❤️", add: false, by: ME }),
    );
    expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual([
      "DELETE /rooms/r1/messages/m-r1-2/reactions/%E2%9D%A4%EF%B8%8F",
    ]);
  });
});

describe("what it leaves in the cache (RCT-01, RCT-06, RCT-08)", () => {
  it("shows the reaction at once, then the list the server answers, in the same place", async () => {
    const wait = pending();
    mockApi({ "POST /rooms/r1/messages/m-r1-2/reactions": () => wait.response as never });
    seed(
      makeMessage({ seq: 1 }),
      makeMessage({ seq: 2, reactions: [{ emoji: "🎉", by: [THEM] }] }),
      makeMessage({ seq: 3 }),
    );
    const { result } = renderHook(() => useToggleReaction("r1"), { wrapper });
    act(() => result.current.mutate({ messageId: "m-r1-2", emoji: "👍", add: true, by: ME }));
    await waitFor(() =>
      expect(reactionsOf(2)).toEqual([
        { emoji: "🎉", by: [THEM] },
        { emoji: "👍", by: [ME] },
      ]),
    );
    expect(reactionsOf(1)).toEqual([]);

    // The server saw someone else's thumb before ours: its list wins.
    await act(async () =>
      wait.release(
        answer([
          { emoji: "🎉", by: [THEM] },
          { emoji: "👍", by: [THEM, ME] },
        ]),
      ),
    );
    await waitFor(() =>
      expect(reactionsOf(2)).toEqual([
        { emoji: "🎉", by: [THEM] },
        { emoji: "👍", by: [THEM, ME] },
      ]),
    );
    const order = qc
      .getQueryData<MessagePages>(queryKeys.roomMessages("r1"))
      ?.pages.flatMap((p) => p.items.map((m) => m.seq));
    expect(order).toEqual([1, 2, 3]);
  });

  it("takes the reaction off at once when it is taken back", async () => {
    const wait = pending();
    mockApi({
      "DELETE /rooms/r1/messages/m-r1-1/reactions/%F0%9F%91%8D": () => wait.response as never,
    });
    seed(makeMessage({ seq: 1, reactions: [{ emoji: "👍", by: [THEM, ME] }] }));
    const { result } = renderHook(() => useToggleReaction("r1"), { wrapper });
    act(() => result.current.mutate({ messageId: "m-r1-1", emoji: "👍", add: false, by: ME }));
    await waitFor(() => expect(reactionsOf(1)).toEqual([{ emoji: "👍", by: [THEM] }]));
    await act(async () => wait.release(answer([{ emoji: "👍", by: [THEM] }])));
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(reactionsOf(1)).toEqual([{ emoji: "👍", by: [THEM] }]);
  });

  it("undoes itself when the request fails, and leaves what others did", async () => {
    mockApi({
      "POST /rooms/r1/messages/m-r1-1/reactions": apiError(403, "forbidden"),
    });
    seed(makeMessage({ seq: 1, reactions: [{ emoji: "🎉", by: [THEM] }] }));
    const { result } = renderHook(() => useToggleReaction("r1"), { wrapper });
    await act(async () => {
      await result.current
        .mutateAsync({ messageId: "m-r1-1", emoji: "👍", add: true, by: ME })
        .catch(() => {});
    });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(reactionsOf(1)).toEqual([{ emoji: "🎉", by: [THEM] }]);
  });

  it("tells the caller about every failure, even when a later toggle followed", async () => {
    const first = pending();
    const failed = vi.fn();
    mockApi({
      "POST /rooms/r1/messages/m-r1-1/reactions": (_url, init) => {
        const emoji = (JSON.parse(String(init?.body)) as { emoji: string }).emoji;
        return emoji === "👍" ? (first.response as never) : answer([{ emoji: "❤️", by: [ME] }]);
      },
    });
    seed(makeMessage({ seq: 1 }));
    const { result } = renderHook(() => useToggleReaction("r1", failed), { wrapper });
    act(() => result.current.mutate({ messageId: "m-r1-1", emoji: "👍", add: true, by: ME }));
    await act(() =>
      result.current.mutateAsync({ messageId: "m-r1-1", emoji: "❤️", add: true, by: ME }),
    );
    expect(failed).not.toHaveBeenCalled();
    await act(async () => first.release(apiError(422, "reaction_limit")));
    await waitFor(() => expect(failed).toHaveBeenCalledTimes(1));
    expect(failed.mock.calls[0]?.[0]).toMatchObject({ status: 422, code: "reaction_limit" });
  });

  it("puts a reaction back when taking it back fails", async () => {
    mockApi({
      "DELETE /rooms/r1/messages/m-r1-1/reactions/%F0%9F%91%8D": apiError(500, "internal"),
    });
    seed(makeMessage({ seq: 1, reactions: [{ emoji: "👍", by: [THEM, ME] }] }));
    const { result } = renderHook(() => useToggleReaction("r1"), { wrapper });
    await act(async () => {
      await result.current
        .mutateAsync({ messageId: "m-r1-1", emoji: "👍", add: false, by: ME })
        .catch(() => {});
    });
    expect(reactionsOf(1)).toEqual([{ emoji: "👍", by: [THEM, ME] }]);
  });

  it("leaves a failed toggle to the last one still on its way", async () => {
    const first = pending();
    const second = pending();
    const failed = vi.fn();
    mockApi({
      "POST /rooms/r1/messages/m-r1-1/reactions": (_url, init) => {
        const emoji = (JSON.parse(String(init?.body)) as { emoji: string }).emoji;
        return (emoji === "👍" ? first : second).response as never;
      },
    });
    seed(makeMessage({ seq: 1 }));
    const { result } = renderHook(() => useToggleReaction("r1", failed), { wrapper });
    act(() => result.current.mutate({ messageId: "m-r1-1", emoji: "👍", add: true, by: ME }));
    act(() => result.current.mutate({ messageId: "m-r1-1", emoji: "❤️", add: true, by: ME }));
    await waitFor(() => expect(reactionsOf(1)).toHaveLength(2));

    // The first failure must not undo itself on top of the second toggle: the second's answer,
    // which knows both, settles the message.
    await act(async () => first.release(apiError(500, "internal")));
    await waitFor(() => expect(failed).toHaveBeenCalledTimes(1));
    expect(reactionsOf(1)).toEqual([
      { emoji: "👍", by: [ME] },
      { emoji: "❤️", by: [ME] },
    ]);
    await act(async () => second.release(answer([{ emoji: "❤️", by: [ME] }])));
    await waitFor(() => expect(reactionsOf(1)).toEqual([{ emoji: "❤️", by: [ME] }]));
  });

  it("does not let the answer of an earlier toggle wipe a later one still on its way", async () => {
    const first = pending();
    const second = pending();
    mockApi({
      "POST /rooms/r1/messages/m-r1-1/reactions": (_url, init) => {
        const emoji = (JSON.parse(String(init?.body)) as { emoji: string }).emoji;
        return (emoji === "👍" ? first : second).response as never;
      },
    });
    seed(makeMessage({ seq: 1 }));
    const { result } = renderHook(() => useToggleReaction("r1"), { wrapper });
    act(() => result.current.mutate({ messageId: "m-r1-1", emoji: "👍", add: true, by: ME }));
    act(() => result.current.mutate({ messageId: "m-r1-1", emoji: "❤️", add: true, by: ME }));
    await waitFor(() => expect(reactionsOf(1)).toHaveLength(2));

    // The answer to the first knows nothing of the second: the heart must stay.
    await act(async () => first.release(answer([{ emoji: "👍", by: [ME] }])));
    await waitFor(() => expect(first.response).resolves.toBeDefined());
    await act(() => new Promise((r) => setTimeout(r, 20)));
    expect(reactionsOf(1)).toEqual([
      { emoji: "👍", by: [ME] },
      { emoji: "❤️", by: [ME] },
    ]);

    await act(async () =>
      second.release(
        answer([
          { emoji: "👍", by: [ME] },
          { emoji: "❤️", by: [ME] },
        ]),
      ),
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(reactionsOf(1)).toEqual([
      { emoji: "👍", by: [ME] },
      { emoji: "❤️", by: [ME] },
    ]);
  });

  it("settles the answers of toggles on different messages independently", async () => {
    const one = pending();
    mockApi({
      "POST /rooms/r1/messages/m-r1-1/reactions": () => one.response as never,
      "POST /rooms/r1/messages/m-r1-2/reactions": answer([{ emoji: "👍", by: [THEM, ME] }]),
    });
    seed(
      makeMessage({ seq: 1 }),
      makeMessage({ seq: 2, reactions: [{ emoji: "👍", by: [THEM] }] }),
    );
    const { result } = renderHook(() => useToggleReaction("r1"), { wrapper });
    act(() => result.current.mutate({ messageId: "m-r1-1", emoji: "👍", add: true, by: ME }));
    await act(() =>
      result.current.mutateAsync({ messageId: "m-r1-2", emoji: "👍", add: true, by: ME }),
    );
    // The second message is settled by its own answer even though the first is still out.
    expect(reactionsOf(2)).toEqual([{ emoji: "👍", by: [THEM, ME] }]);
    expect(reactionsOf(1)).toEqual([{ emoji: "👍", by: [ME] }]);
    await act(async () => one.release(answer([{ emoji: "👍", by: [ME] }])));
  });

  it("writes the reactions on the dashboard feed as well", async () => {
    mockApi({
      "POST /rooms/r1/messages/m-r1-1/reactions": answer([{ emoji: "👍", by: [ME] }]),
    });
    seed(makeMessage({ seq: 1 }));
    qc.setQueryData<ActivityItem[]>(queryKeys.activity, [
      { room: { id: "r1", name: "general" }, message: makeMessage({ room_id: "r1", seq: 1 }) },
    ]);
    const { result } = renderHook(() => useToggleReaction("r1"), { wrapper });
    await act(() =>
      result.current.mutateAsync({ messageId: "m-r1-1", emoji: "👍", add: true, by: ME }),
    );
    expect(qc.getQueryData<ActivityItem[]>(queryKeys.activity)?.[0]?.message.reactions).toEqual([
      { emoji: "👍", by: [ME] },
    ]);
  });
});

describe("rollback under rapid toggles (RCT-08)", () => {
  it("puts the chip back when one toggle fails", async () => {
    mockApi({
      "POST /rooms/r1/messages/m-r1-1/reactions": apiError(500, "internal"),
      "GET /rooms/r1/messages": json(200, {
        items: [makeMessage({ seq: 1, reactions: [{ emoji: "🎉", by: [THEM] }] })],
        has_more: false,
      }),
    });
    seed(makeMessage({ seq: 1, reactions: [{ emoji: "🎉", by: [THEM] }] }));
    const { result } = renderHook(() => useToggleReaction("r1"), { wrapper });
    await act(async () => {
      await result.current
        .mutateAsync({ messageId: "m-r1-1", emoji: "👍", add: true, by: ME })
        .catch(() => {});
    });
    await waitFor(() => expect(reactionsOf(1)).toEqual([{ emoji: "🎉", by: [THEM] }]));
  });

  it("keeps an add that failed from undoing the remove that follows it", async () => {
    const add = pending();
    const remove = pending();
    const failed = vi.fn();
    mockApi({
      "POST /rooms/r1/messages/m-r1-1/reactions": () => add.response as never,
      "DELETE /rooms/r1/messages/m-r1-1/reactions/%F0%9F%91%8D": () => remove.response as never,
    });
    seed(makeMessage({ seq: 1 }));
    const { result } = renderHook(() => useToggleReaction("r1", failed), { wrapper });
    act(() => result.current.mutate({ messageId: "m-r1-1", emoji: "👍", add: true, by: ME }));
    await waitFor(() => expect(reactionsOf(1)).toEqual([{ emoji: "👍", by: [ME] }]));
    act(() => result.current.mutate({ messageId: "m-r1-1", emoji: "👍", add: false, by: ME }));
    await waitFor(() => expect(reactionsOf(1)).toEqual([]));

    // The add fails while the remove is still out: the screen keeps what the remove shows.
    await act(async () => add.release(apiError(500, "internal")));
    await waitFor(() => expect(failed).toHaveBeenCalledTimes(1));
    expect(reactionsOf(1)).toEqual([]);
    await act(async () => remove.release(answer([])));
    await waitFor(() => expect(reactionsOf(1)).toEqual([]));
  });

  it("follows the server when the add succeeds and the later remove fails", async () => {
    const add = pending();
    const remove = pending();
    mockApi({
      "POST /rooms/r1/messages/m-r1-1/reactions": () => add.response as never,
      "DELETE /rooms/r1/messages/m-r1-1/reactions/%F0%9F%91%8D": () => remove.response as never,
      // The server holds a list the failing remove's opposite toggle cannot know: the refetch wins.
      "GET /rooms/r1/messages": json(200, {
        items: [
          makeMessage({
            seq: 1,
            reactions: [
              { emoji: "🎉", by: [THEM] },
              { emoji: "👍", by: [ME] },
            ],
          }),
        ],
        has_more: false,
      }),
    });
    seed(makeMessage({ seq: 1 }));
    const { result } = renderHook(() => useToggleReaction("r1"), { wrapper });
    act(() => result.current.mutate({ messageId: "m-r1-1", emoji: "👍", add: true, by: ME }));
    await waitFor(() => expect(reactionsOf(1)).toEqual([{ emoji: "👍", by: [ME] }]));
    act(() => result.current.mutate({ messageId: "m-r1-1", emoji: "👍", add: false, by: ME }));
    await waitFor(() => expect(reactionsOf(1)).toEqual([]));

    // The add reaches the server, but its answer is held while the remove is still out.
    await act(async () => add.release(answer([{ emoji: "👍", by: [ME] }])));
    await act(() => new Promise((r) => setTimeout(r, 20)));
    expect(reactionsOf(1)).toEqual([]);

    // The remove fails last: the screen takes the server's list, not a local opposite toggle.
    await act(async () => remove.release(apiError(500, "internal")));
    await waitFor(() =>
      expect(reactionsOf(1)).toEqual([
        { emoji: "🎉", by: [THEM] },
        { emoji: "👍", by: [ME] },
      ]),
    );
  });
});

describe("applyReactions (RCT-06)", () => {
  it("leaves a placeholder without reactions, and a timeline that does not hold the message alone", () => {
    seed(
      makeMessage({ seq: 1, text: "", deleted_at: "2026-10-19T14:00:00Z" }),
      makeMessage({ seq: 2 }),
    );
    applyReactions(qc, "r1", "m-r1-1", [{ emoji: "👍", by: [THEM] }]);
    expect(reactionsOf(1)).toEqual([]);
    const before = qc.getQueryData(queryKeys.roomMessages("r1"));
    applyReactions(qc, "r1", "m-r1-9", [{ emoji: "👍", by: [THEM] }]);
    expect(qc.getQueryData(queryKeys.roomMessages("r1"))).toBe(before);
    applyReactions(qc, "elsewhere", "m-elsewhere-1", [{ emoji: "👍", by: [THEM] }]);
    expect(qc.getQueryData(queryKeys.roomMessages("elsewhere"))).toBeUndefined();
  });

  it("leaves a deleted message of the dashboard feed without reactions too", () => {
    qc.setQueryData<ActivityItem[]>(queryKeys.activity, [
      {
        room: { id: "r1", name: "general" },
        message: makeMessage({
          room_id: "r1",
          seq: 1,
          text: "",
          deleted_at: "2026-10-19T14:00:00Z",
        }),
      },
      { room: { id: "r1", name: "general" }, message: makeMessage({ room_id: "r1", seq: 2 }) },
    ]);
    applyReactions(qc, "r1", "m-r1-1", [{ emoji: "👍", by: [THEM] }]);
    applyReactions(qc, "r1", "m-r1-2", [{ emoji: "👍", by: [THEM] }]);
    expect(
      qc.getQueryData<ActivityItem[]>(queryKeys.activity)?.map((a) => a.message.reactions),
    ).toEqual([[], [{ emoji: "👍", by: [THEM] }]]);
  });

  it("changes nothing when the reactions are the ones the message has", () => {
    seed(makeMessage({ seq: 1, reactions: [{ emoji: "👍", by: [THEM] }] }));
    const before = qc.getQueryData(queryKeys.roomMessages("r1"));
    applyReactions(qc, "r1", "m-r1-1", [{ emoji: "👍", by: [THEM] }]);
    expect(qc.getQueryData(queryKeys.roomMessages("r1"))).toBe(before);
  });
});
