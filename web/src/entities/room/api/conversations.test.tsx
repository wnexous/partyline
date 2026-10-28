import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import type { Conversation, Room } from "@/shared/api/types.gen";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeConversation, makeIntelligence, makeRoom } from "@/test/fixtures";
import { useConversations, useMarkRead } from "./conversations";
import { useCreateGroup, useCreateRoom, useDeleteRoom, useOpenDM, useUpdateRoom } from "./rooms";

/*
 * The hooks of the messenger's data layer: what each one sends, and what it leaves in the cache
 * for the sidebar, the conversation page and the profile pages to read.
 */

let qc: QueryClient;
const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={qc}>{children}</QueryClientProvider>
);

beforeEach(() => {
  qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
});
afterEach(() => {
  qc.clear();
  vi.unstubAllGlobals();
});

const atlas = makeIntelligence({
  id: "a1",
  kind: "ai",
  role: "ai",
  display_name: "Atlas",
  handle: "atlas",
});
const stale = (key: readonly unknown[]) => qc.getQueryState(key)?.isInvalidated;

describe("conversation list (DM-06)", () => {
  it("loads GET /conversations as an array", async () => {
    mockApi({
      "GET /conversations": json(200, {
        items: [makeConversation({ id: "r1", unread: 3 }), makeConversation({ id: "r2" })],
      }),
    });
    const { result } = renderHook(() => useConversations(), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.map((c) => [c.id, c.unread])).toEqual([
      ["r1", 3],
      ["r2", 0],
    ]);
  });
});

describe("opening a direct message (DM-01)", () => {
  it.each([
    [201, "a new DM"],
    [200, "the existing DM"],
  ])("posts the intelligence id and answers %i with %s, ready to open", async (status) => {
    const dm = makeRoom({
      id: "r5",
      kind: "dm",
      name: "",
      participants: [makeIntelligence(), atlas],
    });
    const { calls } = mockApi({ "POST /conversations/dm": json(status, dm) });
    qc.setQueryData(queryKeys.conversations, [makeConversation()]);
    qc.setQueryData(queryKeys.rooms, []);
    const { result } = renderHook(() => useOpenDM(), { wrapper });

    let room: Room | undefined;
    await act(async () => {
      room = await result.current.mutateAsync("a1");
    });

    expect(calls[0]).toMatchObject({
      method: "POST",
      path: "/conversations/dm",
      body: { with: "a1" },
    });
    expect(room?.id).toBe("r5");
    expect(qc.getQueryData<Room>(queryKeys.room("r5"))).toEqual(dm);
    // The new DM is a row of the sidebar and of the rooms list.
    expect(stale(queryKeys.conversations)).toBe(true);
    expect(stale(queryKeys.rooms)).toBe(true);
  });

  it("rejects with the API code when the target is unknown or has no route", async () => {
    mockApi({
      "POST /conversations/dm": apiError(404, "contact_not_found", "no such contact"),
    });
    const { result } = renderHook(() => useOpenDM(), { wrapper });
    const failure = await act(() => result.current.mutateAsync("zz").catch((e: unknown) => e));
    expect(failure).toBeInstanceOf(ApiError);
    expect(failure).toMatchObject({ status: 404, code: "contact_not_found" });
  });
});

describe("creating a group (LAY-04)", () => {
  it("posts the name, description and member ids, and caches the new group", async () => {
    const group = makeRoom({ id: "r6", name: "release-crew" });
    const { calls } = mockApi({ "POST /groups": json(201, group) });
    qc.setQueryData(queryKeys.conversations, []);
    qc.setQueryData(queryKeys.rooms, []);
    const { result } = renderHook(() => useCreateGroup(), { wrapper });

    await act(async () => {
      await result.current.mutateAsync({
        name: "release-crew",
        description: "ships things",
        members: ["a1", "u2"],
      });
    });

    expect(calls[0]).toMatchObject({
      method: "POST",
      path: "/groups",
      body: { name: "release-crew", description: "ships things", members: ["a1", "u2"] },
    });
    expect(qc.getQueryData<Room>(queryKeys.room("r6"))).toEqual(group);
    expect(stale(queryKeys.conversations)).toBe(true);
    expect(stale(queryKeys.rooms)).toBe(true);
  });

  it("fails with every refused member and creates nothing in the cache", async () => {
    mockApi({
      "POST /groups": json(422, {
        error: {
          code: "members_refused",
          message: "some members cannot be added",
          refused: [
            { id: "i7", reason: "not_found" },
            { id: "p3", reason: "person" },
          ],
        },
      }),
    });
    qc.setQueryData(queryKeys.conversations, []);
    const { result } = renderHook(() => useCreateGroup(), { wrapper });
    const failure = await act(() =>
      result.current.mutateAsync({ name: "crew", members: ["i7", "p3"] }).catch((e: unknown) => e),
    );
    expect(failure).toMatchObject({
      code: "members_refused",
      refused: [
        { id: "i7", reason: "not_found" },
        { id: "p3", reason: "person" },
      ],
    });
    expect(stale(queryKeys.conversations)).toBe(false);
  });
});

describe("rooms made, renamed, routed or deleted through the room hooks (LAY-01)", () => {
  const seed = () => {
    qc.setQueryData<Conversation[]>(queryKeys.conversations, [
      makeConversation({ id: "r1" }),
      makeConversation({ id: "r2" }),
    ]);
    qc.setQueryData(queryKeys.rooms, []);
  };

  it("a new room is a row of the sidebar", async () => {
    mockApi({ "POST /rooms": json(201, makeRoom({ id: "r8", name: "night-shift" })) });
    seed();
    const { result } = renderHook(() => useCreateRoom(), { wrapper });
    await act(async () => {
      await result.current.mutateAsync({ name: "night-shift" });
    });
    expect(stale(queryKeys.conversations)).toBe(true);
  });

  it("a renamed room is fetched again for the sidebar", async () => {
    mockApi({ "PATCH /rooms/r1": json(200, makeRoom({ id: "r1", name: "renamed" })) });
    seed();
    const { result } = renderHook(() => useUpdateRoom("r1"), { wrapper });
    await act(async () => {
      await result.current.mutateAsync({ name: "renamed" });
    });
    expect(stale(queryKeys.conversations)).toBe(true);
  });

  it("a deleted room leaves the sidebar at once", async () => {
    mockApi({ "DELETE /rooms/r1": new Response(null, { status: 204 }) });
    seed();
    const { result } = renderHook(() => useDeleteRoom(), { wrapper });
    await act(async () => {
      await result.current.mutateAsync("r1");
    });
    expect(qc.getQueryData<Conversation[]>(queryKeys.conversations)?.map((c) => c.id)).toEqual([
      "r2",
    ]);
  });
});

describe("marking a conversation read (DM-07)", () => {
  const seed = () =>
    qc.setQueryData<Conversation[]>(queryKeys.conversations, [
      makeConversation({ id: "r1", unread: 4 }),
      makeConversation({ id: "r2", unread: 1 }),
    ]);

  it("puts the newest seq and shows the unread count the server holds", async () => {
    const { calls } = mockApi({
      "PUT /rooms/r1/read": json(200, { room_id: "r1", seq: 9, unread: 0 }),
    });
    seed();
    const { result } = renderHook(() => useMarkRead(), { wrapper });
    await act(async () => {
      await result.current.mutateAsync({ roomId: "r1", seq: 9 });
    });
    expect(calls[0]).toMatchObject({ method: "PUT", path: "/rooms/r1/read", body: { seq: 9 } });
    const list = qc.getQueryData<Conversation[]>(queryKeys.conversations);
    expect(list?.map((c) => [c.id, c.unread])).toEqual([
      ["r1", 0],
      ["r2", 1],
    ]);
  });

  it("reads up to the newest message when no seq is given", async () => {
    const { calls } = mockApi({
      "PUT /rooms/r1/read": json(200, { room_id: "r1", seq: 12, unread: 0 }),
    });
    seed();
    const { result } = renderHook(() => useMarkRead(), { wrapper });
    await act(async () => {
      await result.current.mutateAsync({ roomId: "r1" });
    });
    expect(calls[0]).toMatchObject({ method: "PUT", path: "/rooms/r1/read", body: {} });
  });

  it("keeps what is still unread after a position behind the newest message", async () => {
    mockApi({ "PUT /rooms/r1/read": json(200, { room_id: "r1", seq: 7, unread: 2 }) });
    seed();
    const { result } = renderHook(() => useMarkRead(), { wrapper });
    await act(async () => {
      await result.current.mutateAsync({ roomId: "r1", seq: 7 });
    });
    expect(qc.getQueryData<Conversation[]>(queryKeys.conversations)?.[0]?.unread).toBe(2);
  });

  it("never raises a count that a newer answer already lowered", async () => {
    mockApi({ "PUT /rooms/r1/read": json(200, { room_id: "r1", seq: 5, unread: 3 }) });
    qc.setQueryData<Conversation[]>(queryKeys.conversations, [
      makeConversation({ id: "r1", unread: 0 }),
    ]);
    const { result } = renderHook(() => useMarkRead(), { wrapper });
    await act(async () => {
      await result.current.mutateAsync({ roomId: "r1", seq: 5 });
    });
    expect(qc.getQueryData<Conversation[]>(queryKeys.conversations)?.[0]?.unread).toBe(0);
  });
});
