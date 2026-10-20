import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { type MessagePages, queryKeys } from "@/lib/api/queries";
import type { Agent, Connector, Identity, LiveEvent, RoomMember } from "@/lib/api/types.gen";
import { makeMessage } from "@/test/fixtures";
import { handleLiveEvent, type LiveActions } from "./handlers";
import { useLiveStore } from "./store";

function seedTimeline(qc: QueryClient, roomId: string, seqs: number[]) {
  const data: MessagePages = {
    pages: [{ items: seqs.map((seq) => makeMessage({ room_id: roomId, seq })), has_more: false }],
    pageParams: [undefined],
  };
  qc.setQueryData(queryKeys.roomMessages(roomId), data);
}

const timelineSeqs = (qc: QueryClient, roomId: string) =>
  qc
    .getQueryData<MessagePages>(queryKeys.roomMessages(roomId))
    ?.pages.flatMap((p) => p.items.map((m) => m.seq));

const actions = (): LiveActions => useLiveStore.getState();

describe("live handlers", () => {
  let qc: QueryClient;

  beforeEach(() => {
    qc = new QueryClient();
    useLiveStore.getState().resetLive();
  });

  afterEach(() => qc.clear());

  describe("room.message (UI-04)", () => {
    it("appends the message to the open room's newest page", () => {
      seedTimeline(qc, "r1", [1, 2]);
      const m = makeMessage({ room_id: "r1", seq: 3, text: "deploy done" });
      handleLiveEvent(qc, actions(), { type: "room.message", data: m });
      const data = qc.getQueryData<MessagePages>(queryKeys.roomMessages("r1"));
      expect(data?.pages[0]?.items.at(-1)).toEqual(m);
      expect(timelineSeqs(qc, "r1")).toEqual([1, 2, 3]);
    });

    it("does not duplicate a message delivered twice or already fetched", () => {
      seedTimeline(qc, "r1", [1, 2]);
      const event: LiveEvent = {
        type: "room.message",
        data: makeMessage({ room_id: "r1", seq: 3 }),
      };
      handleLiveEvent(qc, actions(), event);
      handleLiveEvent(qc, actions(), event);
      handleLiveEvent(qc, actions(), {
        type: "room.message",
        data: makeMessage({ room_id: "r1", seq: 2 }),
      });
      expect(timelineSeqs(qc, "r1")).toEqual([1, 2, 3]);
    });

    it("ignores messages for rooms whose timeline is not cached", () => {
      handleLiveEvent(qc, actions(), {
        type: "room.message",
        data: makeMessage({ room_id: "elsewhere", seq: 9 }),
      });
      expect(qc.getQueryData(queryKeys.roomMessages("elsewhere"))).toBeUndefined();
    });
  });

  it("lagged invalidates every cached query", () => {
    seedTimeline(qc, "r1", [1]);
    qc.setQueryData(queryKeys.rooms, []);
    handleLiveEvent(qc, actions(), { type: "lagged" });
    expect(qc.getQueryState(queryKeys.roomMessages("r1"))?.isInvalidated).toBe(true);
    expect(qc.getQueryState(queryKeys.rooms)?.isInvalidated).toBe(true);
  });

  it("agent.presence updates the store, the agents list and room members (UI-15)", () => {
    qc.setQueryData<Agent[]>(queryKeys.agents, [
      { id: "a1", online: false, name: "builder" } as Agent,
      { id: "a2", online: false, name: "reviewer" } as Agent,
    ]);
    qc.setQueryData<RoomMember[]>(queryKeys.roomMembers("r1"), [
      { id: "mb1", kind: "agent", member_id: "a1", online: false } as RoomMember,
    ]);
    handleLiveEvent(qc, actions(), {
      type: "agent.presence",
      data: { agent_id: "a1", online: true, last_seen_at: "2026-10-19T12:05:00Z" },
    });
    expect(useLiveStore.getState().presence.a1).toEqual({
      online: true,
      last_seen_at: "2026-10-19T12:05:00Z",
    });
    const agents = qc.getQueryData<Agent[]>(queryKeys.agents);
    expect(agents?.map((a) => [a.id, a.online])).toEqual([
      ["a1", true],
      ["a2", false],
    ]);
    expect(qc.getQueryData<RoomMember[]>(queryKeys.roomMembers("r1"))?.[0]?.online).toBe(true);
  });

  it("agent.presence for an agent missing from the cached list refetches the list (UI-14)", () => {
    qc.setQueryData<Agent[]>(queryKeys.agents, [{ id: "a1", online: false } as Agent]);
    handleLiveEvent(qc, actions(), {
      type: "agent.presence",
      data: { agent_id: "a9", online: true },
    });
    expect(qc.getQueryState(queryKeys.agents)?.isInvalidated).toBe(true);
  });

  it("agent.presence for a cached agent patches without refetching", () => {
    qc.setQueryData<Agent[]>(queryKeys.agents, [{ id: "a1", online: false } as Agent]);
    handleLiveEvent(qc, actions(), {
      type: "agent.presence",
      data: { agent_id: "a1", online: true },
    });
    expect(qc.getQueryState(queryKeys.agents)?.isInvalidated).toBe(false);
  });

  it("connector.status updates the store and the cached connector (UI-17)", () => {
    qc.setQueryData<Connector>(queryKeys.connector("c1"), {
      id: "c1",
      status: "connected",
    } as Connector);
    handleLiveEvent(qc, actions(), {
      type: "connector.status",
      data: { connector_id: "c1", status: "error", last_error: "token rejected" },
    });
    expect(useLiveStore.getState().connectors.c1).toEqual({
      status: "error",
      last_error: "token rejected",
    });
    expect(qc.getQueryData<Connector>(queryKeys.connector("c1"))).toMatchObject({
      status: "error",
      last_error: "token rejected",
    });
  });

  it("outbox.status updates the delivery chip of the matching message", () => {
    seedTimeline(qc, "r1", [1, 2]);
    handleLiveEvent(qc, actions(), {
      type: "outbox.status",
      data: {
        id: "o1",
        room_id: "r1",
        message_id: "m-r1-2",
        chat_id: "ch1",
        status: "sent",
        attempts: 1,
        updated_at: "2026-10-19T12:00:01Z",
      },
    });
    const items = qc.getQueryData<MessagePages>(queryKeys.roomMessages("r1"))?.pages[0]?.items;
    expect(items?.[1]?.outbox).toEqual([
      { id: "o1", chat_id: "ch1", status: "sent", attempts: 1, updated_at: "2026-10-19T12:00:01Z" },
    ]);
    expect(items?.[0]?.outbox).toBeUndefined();
  });

  it("identity.linked records the identity and refetches identities (UI-18)", () => {
    qc.setQueryData(queryKeys.identities, []);
    const identity = { id: "i1", connector_kind: "telegram", display: "Ana" } as Identity;
    handleLiveEvent(qc, actions(), { type: "identity.linked", data: identity });
    expect(useLiveStore.getState().lastLinkedIdentity).toEqual(identity);
    expect(qc.getQueryState(queryKeys.identities)?.isInvalidated).toBe(true);
  });

  it("room.members invalidates that room's member list only", () => {
    qc.setQueryData(queryKeys.roomMembers("r1"), []);
    qc.setQueryData(queryKeys.roomMembers("r2"), []);
    handleLiveEvent(qc, actions(), { type: "room.members", data: { room_id: "r1" } });
    expect(qc.getQueryState(queryKeys.roomMembers("r1"))?.isInvalidated).toBe(true);
    expect(qc.getQueryState(queryKeys.roomMembers("r2"))?.isInvalidated).toBe(false);
  });

  it("room.deleted drops the room from the cache", () => {
    qc.setQueryData(queryKeys.rooms, [{ id: "r1" }, { id: "r2" }]);
    seedTimeline(qc, "r1", [1]);
    handleLiveEvent(qc, actions(), { type: "room.deleted", data: { room_id: "r1" } });
    expect(qc.getQueryData<{ id: string }[]>(queryKeys.rooms)).toEqual([{ id: "r2" }]);
    expect(qc.getQueryData(queryKeys.roomMessages("r1"))).toBeUndefined();
  });
});
