import { QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useReact } from "@/hooks/use-react";
import { MORE_REACTIONS } from "@/lib/reactions";
import type { MessagePages } from "@/shared/api/cache-types";
import { queryKeys } from "@/shared/api/query-keys";
import type { Message, Reaction } from "@/shared/api/types.gen";
import { MaxMessageEmoji } from "@/shared/api/types.gen";
import { I18nProvider } from "@/shared/i18n";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMessage } from "@/test/fixtures";
import { makeQueryClient } from "@/test/render";

// Only `toast` is ours; the rest of the module stays.
const toast = vi.hoisted(() => Object.assign(vi.fn(), { error: vi.fn() }));
vi.mock("sonner", async (importOriginal) => ({
  ...(await importOriginal<typeof import("sonner")>()),
  toast,
}));

/*
 * What a person's tap on a reaction does (RCT-01, RCT-04, RCT-08): it adds the reaction or takes it back
 * according to what the message holds now, it says so when the message is full or the request fails, and it
 * leaves the screen as it was after a failure.
 */

const ME = "u1";
const THEM = "u2";

let qc: ReturnType<typeof makeQueryClient>;
const wrapperFor =
  (locale: "en" | "pt-BR") =>
  ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>
      <I18nProvider locale={locale}>{children}</I18nProvider>
    </QueryClientProvider>
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

beforeEach(() => {
  qc = makeQueryClient();
  toast.mockClear();
  toast.error.mockClear();
});

describe("a tap on a reaction (RCT-01, RCT-03)", () => {
  it("adds the reaction when the viewer has not used the emoji", async () => {
    const message = makeMessage({ seq: 1 });
    seed(message);
    const { calls } = mockApi({
      "POST /rooms/r1/messages/m-r1-1/reactions": json(200, { items: [{ emoji: "👍", by: [ME] }] }),
    });
    const { result } = renderHook(() => useReact("r1", ME), { wrapper: wrapperFor("en") });
    act(() => result.current(message, "👍"));
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0]).toMatchObject({ method: "POST", body: { emoji: "👍" } });
    expect(reactionsOf(1)).toEqual([{ emoji: "👍", by: [ME] }]);
  });

  it("takes the reaction back when the viewer used the emoji already", async () => {
    const message = makeMessage({ seq: 1, reactions: [{ emoji: "👍", by: [THEM, ME] }] });
    seed(message);
    const { calls } = mockApi({
      "DELETE /rooms/r1/messages/m-r1-1/reactions/%F0%9F%91%8D": json(200, {
        items: [{ emoji: "👍", by: [THEM] }],
      }),
    });
    const { result } = renderHook(() => useReact("r1", ME), { wrapper: wrapperFor("en") });
    act(() => result.current(message, "👍"));
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(calls[0]?.method).toBe("DELETE");
    expect(reactionsOf(1)).toEqual([{ emoji: "👍", by: [THEM] }]);
  });

  it("goes by what the cache holds now, not by the message it was handed", async () => {
    // A second tap before the screen drew the first: the handed message is still the old one.
    const stale = makeMessage({ seq: 1 });
    seed(stale);
    const { calls } = mockApi({
      "POST /rooms/r1/messages/m-r1-1/reactions": json(200, { items: [{ emoji: "👍", by: [ME] }] }),
      "DELETE /rooms/r1/messages/m-r1-1/reactions/%F0%9F%91%8D": json(200, { items: [] }),
    });
    const { result } = renderHook(() => useReact("r1", ME), { wrapper: wrapperFor("en") });
    act(() => result.current(stale, "👍"));
    act(() => result.current(stale, "👍"));
    await waitFor(() => expect(calls.map((c) => c.method)).toEqual(["POST", "DELETE"]));
    await waitFor(() => expect(reactionsOf(1)).toEqual([]));
  });

  it("is the same callback from one render to the next", () => {
    seed(makeMessage({ seq: 1 }));
    const { result, rerender } = renderHook(() => useReact("r1", ME), {
      wrapper: wrapperFor("en"),
    });
    const first = result.current;
    rerender();
    expect(result.current).toBe(first);
  });
});

describe("a message that holds the most emoji (RCT-04)", () => {
  const full = (): Reaction[] =>
    MORE_REACTIONS.slice(0, MaxMessageEmoji).map((emoji) => ({ emoji, by: [THEM] }));

  it("says so for a new emoji and sends nothing", () => {
    const message = makeMessage({ seq: 1, reactions: full() });
    seed(message);
    const { calls } = mockApi({});
    const { result } = renderHook(() => useReact("r1", ME), { wrapper: wrapperFor("en") });
    act(() => result.current(message, "🍕"));
    expect(calls).toHaveLength(0);
    expect(toast.error).toHaveBeenCalledWith("A message holds at most 12 different reactions.");
    expect(reactionsOf(1)).toEqual(full());
  });

  it("still lets the viewer join an emoji it has", async () => {
    const message = makeMessage({ seq: 1, reactions: full() });
    seed(message);
    const first = MORE_REACTIONS[0] as string;
    const { calls } = mockApi({
      "POST /rooms/r1/messages/m-r1-1/reactions": json(200, { items: full() }),
    });
    const { result } = renderHook(() => useReact("r1", ME), { wrapper: wrapperFor("en") });
    act(() => result.current(message, first));
    await waitFor(() => expect(calls).toHaveLength(1));
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("says so in Portuguese", () => {
    const message = makeMessage({ seq: 1, reactions: full() });
    seed(message);
    mockApi({});
    const { result } = renderHook(() => useReact("r1", ME), { wrapper: wrapperFor("pt-BR") });
    act(() => result.current(message, "🍕"));
    expect(toast.error).toHaveBeenCalledWith("Uma mensagem tem no máximo 12 reações diferentes.");
  });

  it("says so when the server says it, as it can when someone else took the last place", async () => {
    const message = makeMessage({ seq: 1 });
    seed(message);
    mockApi({ "POST /rooms/r1/messages/m-r1-1/reactions": apiError(422, "reaction_limit") });
    const { result } = renderHook(() => useReact("r1", ME), { wrapper: wrapperFor("en") });
    act(() => result.current(message, "👍"));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("A message holds at most 12 different reactions."),
    );
    expect(reactionsOf(1)).toEqual([]);
  });
});

describe("a failure (RCT-08)", () => {
  it("undoes the reaction and says so", async () => {
    const message = makeMessage({ seq: 1, reactions: [{ emoji: "🎉", by: [THEM] }] });
    seed(message);
    mockApi({ "POST /rooms/r1/messages/m-r1-1/reactions": apiError(500, "internal") });
    const { result } = renderHook(() => useReact("r1", ME), { wrapper: wrapperFor("en") });
    act(() => result.current(message, "👍"));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn’t save your reaction."));
    expect(toast.error).toHaveBeenCalledTimes(1);
    expect(reactionsOf(1)).toEqual([{ emoji: "🎉", by: [THEM] }]);
  });

  it("says so in Portuguese", async () => {
    const message = makeMessage({ seq: 1 });
    seed(message);
    mockApi({ "POST /rooms/r1/messages/m-r1-1/reactions": apiError(500, "internal") });
    const { result } = renderHook(() => useReact("r1", ME), { wrapper: wrapperFor("pt-BR") });
    act(() => result.current(message, "👍"));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Não foi possível salvar sua reação."),
    );
  });
});
