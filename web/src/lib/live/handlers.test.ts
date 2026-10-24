import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  ACTIVITY_PER_ROOM,
  ACTIVITY_SIZE,
  type MessagePages,
  type PeoplePages,
  queryKeys,
} from "@/lib/api/queries";
import type {
  ActivityItem,
  Agent,
  Connector,
  Contacts,
  Conversation,
  Identity,
  IntelligenceProfile,
  IntelligenceRef,
  LiveEvent,
  Me,
  Message,
  Room,
  RoomMember,
} from "@/lib/api/types.gen";
import {
  makeAI,
  makeAIContact,
  makeConnector,
  makeContacts,
  makeConversation,
  makeIntelligence,
  makeMe,
  makeMember,
  makeMessage,
  makeOrgAIs,
  makePerson,
  makeRoom,
} from "@/test/fixtures";
import { catchUp, handleLiveEvent, type LiveActions } from "./handlers";
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
      { id: "a1", online: false } as Agent,
      { id: "a2", online: false } as Agent,
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
  describe("dashboard activity (UI-04 on the overview)", () => {
    const item = (over: Partial<Message> = {}, name = `room ${over.room_id ?? "r1"}`) => ({
      room: { id: over.room_id ?? "r1", name },
      message: makeMessage(over),
    });
    const feed = () => qc.getQueryData<ActivityItem[]>(queryKeys.activity);
    const activityIds = () => feed()?.map((a) => a.message.id);

    it("puts a live message first in the cached activity feed, once", () => {
      qc.setQueryData(queryKeys.activity, [
        item({ room_id: "r1", seq: 1, created_at: "2026-10-19T12:00:00Z" }),
        item({ room_id: "r2", seq: 3, created_at: "2026-10-19T11:00:00Z" }, "deploys"),
      ]);
      const m = makeMessage({ room_id: "r2", seq: 7, created_at: "2026-10-19T12:01:00Z" });
      handleLiveEvent(qc, actions(), { type: "room.message", data: m });
      handleLiveEvent(qc, actions(), { type: "room.message", data: m });
      expect(activityIds()).toEqual(["m-r2-7", "m-r1-1", "m-r2-3"]);
      // The room name comes from what the feed already knows about that room.
      expect(feed()?.[0]?.room).toEqual({ id: "r2", name: "deploys" });
    });

    it("names a live message's room from the cached rooms list", () => {
      qc.setQueryData(queryKeys.activity, [item({ room_id: "r1", seq: 1 })]);
      qc.setQueryData(queryKeys.rooms, [makeRoom({ id: "r5", name: "night-shift" })]);
      handleLiveEvent(qc, actions(), {
        type: "room.message",
        data: makeMessage({ room_id: "r5", seq: 1, created_at: "2026-10-19T12:30:00Z" }),
      });
      expect(feed()?.[0]?.room).toEqual({ id: "r5", name: "night-shift" });
    });

    it("refetches the feed when a live message names a room it cannot name", () => {
      qc.setQueryData(queryKeys.activity, [item({ room_id: "r1", seq: 1 })]);
      handleLiveEvent(qc, actions(), {
        type: "room.message",
        data: makeMessage({ room_id: "r9", seq: 1, created_at: "2026-10-19T12:30:00Z" }),
      });
      expect(qc.getQueryState(queryKeys.activity)?.isInvalidated).toBe(true);
      expect(activityIds()).toEqual(["m-r1-1"]);
    });

    it("keeps at most ACTIVITY_SIZE messages, dropping the oldest", () => {
      // One message per room, so only the total cap applies.
      qc.setQueryData(
        queryKeys.activity,
        Array.from({ length: ACTIVITY_SIZE }, (_, i) =>
          item({ room_id: `r${i}`, seq: 1, created_at: `2026-10-19T11:${10 + i}:00Z` }),
        ).reverse(),
      );
      qc.setQueryData(queryKeys.rooms, [makeRoom({ id: "rx" })]);
      handleLiveEvent(qc, actions(), {
        type: "room.message",
        data: makeMessage({ room_id: "rx", seq: 1, created_at: "2026-10-19T12:30:00Z" }),
      });
      const ids = activityIds();
      expect(ids).toHaveLength(ACTIVITY_SIZE);
      expect(ids?.[0]).toBe("m-rx-1");
      expect(ids).not.toContain("m-r0-1");
    });

    it("keeps at most ACTIVITY_PER_ROOM messages of one room, so busy rooms do not hide others", () => {
      qc.setQueryData(queryKeys.activity, [
        ...Array.from({ length: ACTIVITY_PER_ROOM }, (_, i) =>
          item({ room_id: "busy", seq: 10 - i, created_at: `2026-10-19T11:5${9 - i}:00Z` }),
        ),
        item({ room_id: "calm", seq: 1, created_at: "2026-10-19T09:00:00Z" }),
      ]);
      handleLiveEvent(qc, actions(), {
        type: "room.message",
        data: makeMessage({ room_id: "busy", seq: 11, created_at: "2026-10-19T12:00:00Z" }),
      });
      const ids = activityIds() ?? [];
      expect(ids[0]).toBe("m-busy-11");
      expect(ids.filter((id) => id.startsWith("m-busy-"))).toHaveLength(ACTIVITY_PER_ROOM);
      expect(ids).not.toContain(`m-busy-${10 - ACTIVITY_PER_ROOM + 1}`);
      expect(ids).toContain("m-calm-1");
    });

    it("does not create the feed when the dashboard never loaded it", () => {
      handleLiveEvent(qc, actions(), { type: "room.message", data: makeMessage() });
      expect(qc.getQueryData(queryKeys.activity)).toBeUndefined();
    });

    it("room.changed renames that room in the feed", () => {
      qc.setQueryData(queryKeys.activity, [item({ room_id: "r1", seq: 1 }, "old name")]);
      handleLiveEvent(qc, actions(), {
        type: "room.changed",
        data: makeRoom({ id: "r1", name: "new name" }),
      });
      expect(feed()?.[0]?.room).toEqual({ id: "r1", name: "new name" });
    });

    it("room.deleted removes that room's messages from the feed", () => {
      qc.setQueryData(queryKeys.activity, [
        item({ room_id: "r1", seq: 1 }),
        item({ room_id: "r2", seq: 1 }),
      ]);
      handleLiveEvent(qc, actions(), { type: "room.deleted", data: { room_id: "r1" } });
      expect(activityIds()).toEqual(["m-r2-1"]);
    });
  });

  describe("conversation list (LAY-02, DM-06)", () => {
    const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
    const atlas = makeIntelligence({
      id: "a1",
      kind: "ai",
      role: "ai",
      display_name: "Atlas",
      handle: "atlas",
    });
    // Neither a user nor an AI nor a person: system messages and chat senders without a profile.
    const nobody = {
      id: "",
      kind: "",
      role: "",
      display_name: "Partyline",
      handle: "",
    } as unknown as IntelligenceRef;

    const list = () => qc.getQueryData<Conversation[]>(queryKeys.conversations) ?? [];
    const order = () => list().map((c) => c.id);
    const row = (id: string) => list().find((c) => c.id === id);
    const stale = () => qc.getQueryState(queryKeys.conversations)?.isInvalidated;

    /** What the server lists: newest activity first. r2 is a DM with Atlas and has 2 unread. */
    function seed() {
      qc.setQueryData<Conversation[]>(queryKeys.conversations, [
        makeConversation({
          id: "r1",
          title: "general",
          activity_at: "2026-10-19T12:00:00Z",
          last_message: {
            seq: 9,
            author_id: "u2",
            author_name: "Bob Lima",
            text: "morning",
            created_at: "2026-10-19T12:00:00Z",
          },
        }),
        makeConversation({
          id: "r2",
          kind: "dm",
          title: "Atlas",
          counterpart: atlas,
          unread: 2,
          activity_at: "2026-10-19T11:00:00Z",
          last_message: {
            seq: 4,
            author_id: "a1",
            author_name: "",
            text: "done",
            created_at: "2026-10-19T11:00:00Z",
          },
        }),
        makeConversation({
          id: "r3",
          title: "deploys",
          activity_at: "2026-10-19T10:00:00Z",
          last_message: {
            seq: 7,
            author_id: "u2",
            author_name: "Bob Lima",
            text: "rolling out",
            created_at: "2026-10-19T10:00:00Z",
          },
        }),
      ]);
    }

    /** A live message in r3 from Bob (not the user) unless overridden. */
    function post(over: Partial<Message> = {}) {
      handleLiveEvent(qc, actions(), {
        type: "room.message",
        data: makeMessage({
          room_id: "r3",
          seq: 8,
          author_kind: "user",
          author_id: "u2",
          author_name: "Bob Lima",
          author: bob,
          text: "ship it",
          created_at: "2026-10-19T13:00:00Z",
          ...over,
        }),
      });
    }

    beforeEach(() => {
      qc.setQueryData<Me>(queryKeys.me, makeMe());
      seed();
    });

    it("moves a conversation to the top with the new preview, sender and time (LAY-02)", () => {
      post();
      expect(order()).toEqual(["r3", "r1", "r2"]);
      expect(row("r3")).toMatchObject({
        activity_at: "2026-10-19T13:00:00Z",
        last_message: {
          seq: 8,
          author_id: "u2",
          author_name: "Bob Lima",
          text: "ship it",
          created_at: "2026-10-19T13:00:00Z",
        },
      });
    });

    it("adds one unread per message from someone else, only to its own conversation (LAY-02, DM-06)", () => {
      post({ seq: 8 });
      post({ seq: 9, created_at: "2026-10-19T13:01:00Z" });
      expect(row("r3")?.unread).toBe(2);
      expect(row("r1")?.unread).toBe(0);
      expect(row("r2")?.unread).toBe(2);
    });

    it("adds no unread to the open conversation, which still moves up (LAY-02)", () => {
      useLiveStore.setState({ openRoomId: "r3" });
      post();
      expect(order()[0]).toBe("r3");
      expect(row("r3")?.last_message?.seq).toBe(8);
      expect(row("r3")?.unread).toBe(0);
      // Only the open conversation is exempt.
      post({ room_id: "r2", seq: 5, author: atlas, author_id: "a1", author_kind: "agent" });
      expect(row("r2")?.unread).toBe(3);
    });

    it("never counts the user's own messages, typed in the app or on a linked platform account (DM-06)", () => {
      const me = makeIntelligence();
      post({ seq: 8, author: me, author_id: "u1", author_name: "Ana Souza" });
      post({
        seq: 9,
        author_kind: "chat",
        author_id: "c1",
        author_name: "Ops on call",
        author: me,
        created_at: "2026-10-19T13:01:00Z",
      });
      expect(row("r3")?.unread).toBe(0);
      expect(row("r3")?.last_message?.seq).toBe(9);
      expect(order()[0]).toBe("r3");
    });

    it("counts messages from AIs, people, the system and unattributed chat senders (DM-06)", () => {
      const lia = makeIntelligence({
        id: "p1",
        role: "person",
        platform: "whatsapp",
        display_name: "Lia",
        handle: "lia",
      });
      post({ seq: 8, author_kind: "agent", author_id: "a1", author: atlas });
      post({ seq: 9, author_kind: "chat", author_id: "c1", author: lia });
      post({ seq: 10, author_kind: "chat", author_id: "c1", author: nobody });
      post({ seq: 11, author_kind: "system", author_id: undefined, author: nobody });
      expect(row("r3")?.unread).toBe(4);
    });

    it("counts a message delivered twice once, and ignores one older than the preview", () => {
      post({ seq: 8 });
      post({ seq: 8 });
      post({ seq: 6 });
      expect(row("r3")?.unread).toBe(1);
      expect(row("r3")?.last_message?.seq).toBe(8);
    });

    it("previews the first 80 characters, a [file] for a message without text, and the sender only in groups (LAY-01)", () => {
      const long = `${"é".repeat(79)}🙂${"x".repeat(20)}`;
      post({ seq: 8, text: long });
      expect(row("r3")?.last_message?.text).toBe(`${"é".repeat(79)}🙂`);

      post({
        seq: 9,
        text: "  ",
        attachments: [
          { id: "f1", name: "plan.pdf", mime: "application/pdf", size: 10, status: "stored" },
          { id: "f2", name: "other.png", mime: "image/png", size: 10, status: "stored" },
        ],
      });
      expect(row("r3")?.last_message?.text).toBe("[plan.pdf]");

      post({
        seq: 10,
        text: "see attached",
        attachments: [
          { id: "f3", name: "plan.pdf", mime: "application/pdf", size: 10, status: "stored" },
        ],
      });
      expect(row("r3")?.last_message?.text).toBe("see attached");

      // A DM preview carries no sender prefix: the other person is the title.
      post({ room_id: "r2", seq: 5, author: atlas, author_id: "a1", author_name: "Atlas" });
      expect(row("r2")?.last_message).toMatchObject({ author_id: "a1", author_name: "" });
    });

    it("room.read clears the unread count and keeps the order (DM-06, DM-07)", () => {
      handleLiveEvent(qc, actions(), { type: "room.read", data: { room_id: "r2", seq: 4 } });
      expect(row("r2")?.unread).toBe(0);
      expect(order()).toEqual(["r1", "r2", "r3"]);
    });

    it("room.read at an older position leaves the later messages unread (DM-07)", () => {
      qc.setQueryData<Conversation[]>(queryKeys.conversations, [
        makeConversation({
          id: "r2",
          unread: 3,
          last_message: {
            seq: 7,
            author_id: "u2",
            author_name: "Bob Lima",
            text: "last",
            created_at: "2026-10-19T11:00:00Z",
          },
        }),
      ]);
      handleLiveEvent(qc, actions(), { type: "room.read", data: { room_id: "r2", seq: 5 } });
      expect(row("r2")?.unread).toBe(2);
    });

    it("refetches the list when a message names a conversation it does not have (LAY-02)", () => {
      post({ room_id: "r9", seq: 1 });
      expect(stale()).toBe(true);
      expect(order()).toEqual(["r1", "r2", "r3"]);
    });

    it("asks once per unknown conversation: rooms the list never shows do not cost a request per message", () => {
      post({ room_id: "r9", seq: 1 });
      expect(stale()).toBe(true);
      seed(); // writing the list clears the flag, as the refetch's answer would
      expect(stale()).toBe(false);
      post({ room_id: "r9", seq: 2 });
      expect(stale()).toBe(false);
      post({ room_id: "r8", seq: 1 });
      expect(stale()).toBe(true);
    });

    it("does not create a list the sidebar never loaded", () => {
      qc.clear();
      post();
      expect(qc.getQueryData(queryKeys.conversations)).toBeUndefined();
    });

    it("room.changed renames a group and updates the level, and a DM keeps its counterpart as title", () => {
      handleLiveEvent(qc, actions(), {
        type: "room.changed",
        data: makeRoom({ id: "r3", name: "releases", my_level: "view" }),
      });
      expect(row("r3")).toMatchObject({ title: "releases", my_level: "view" });
      handleLiveEvent(qc, actions(), {
        type: "room.changed",
        data: makeRoom({ id: "r2", kind: "dm", name: "", my_level: "post" }),
      });
      expect(row("r2")).toMatchObject({ title: "Atlas", my_level: "post" });
      expect(stale()).toBe(false);
    });

    it("room.changed files the conversation under the organizations the frame names (ORG-02)", () => {
      expect(row("r2")?.org_ids).toEqual(["o1"]);
      handleLiveEvent(qc, actions(), {
        type: "room.changed",
        data: makeRoom({ id: "r2", kind: "dm", name: "", org_ids: ["o1", "o2"] }),
      });
      expect(row("r2")?.org_ids).toEqual(["o1", "o2"]);
      handleLiveEvent(qc, actions(), {
        type: "room.changed",
        data: makeRoom({ id: "r2", kind: "dm", name: "", org_ids: [] }),
      });
      expect(row("r2")?.org_ids).toEqual([]);
    });

    it("room.changed for a conversation the list does not have refetches it, every time", () => {
      handleLiveEvent(qc, actions(), { type: "room.changed", data: makeRoom({ id: "r7" }) });
      expect(stale()).toBe(true);
      seed();
      expect(stale()).toBe(false);
      handleLiveEvent(qc, actions(), { type: "room.changed", data: makeRoom({ id: "r7" }) });
      expect(stale()).toBe(true);
    });

    it("room.deleted removes the conversation", () => {
      handleLiveEvent(qc, actions(), { type: "room.deleted", data: { room_id: "r2" } });
      expect(order()).toEqual(["r1", "r3"]);
    });

    it("member changes, sharing changes and reconnects refresh the list, whose member names feed the search", async () => {
      handleLiveEvent(qc, actions(), { type: "room.members", data: { room_id: "r1" } });
      expect(stale()).toBe(true);
      seed();
      handleLiveEvent(qc, actions(), {
        type: "access.changed",
        data: { resource_kind: "room", resource_id: "r1" },
      });
      expect(stale()).toBe(true);
      seed();
      expect(stale()).toBe(false);
      await catchUp(qc);
      expect(stale()).toBe(true);
    });
  });

  describe("intelligence.changed (PROF-03)", () => {
    const atlas = makeIntelligence({
      id: "a1",
      kind: "ai",
      role: "ai",
      display_name: "Atlas",
      handle: "atlas",
      avatar_url: "/api/v1/intelligences/a1/avatar?v=1",
    });
    const renamed = makeIntelligence({
      id: "a1",
      kind: "ai",
      role: "ai",
      display_name: "Atlas Prime",
      handle: "atlas-prime",
      avatar_url: "/api/v1/intelligences/a1/avatar?v=2",
    });
    const change = (data: IntelligenceRef = renamed) =>
      handleLiveEvent(qc, actions(), { type: "intelligence.changed", data });

    const byAtlas = (room: string, seq: number) =>
      makeMessage({
        room_id: room,
        seq,
        author_kind: "agent",
        author_id: "a1",
        author_name: "Atlas",
        author: atlas,
      });
    const byBob = (room: string, seq: number) =>
      makeMessage({
        room_id: room,
        seq,
        author_id: "u2",
        author_name: "Bob Lima",
        author: makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" }),
      });
    const messages = (room: string) =>
      qc.getQueryData<MessagePages>(queryKeys.roomMessages(room))?.pages.flatMap((p) => p.items) ??
      [];
    const seedMessages = (room: string, items: Message[]) =>
      qc.setQueryData<MessagePages>(queryKeys.roomMessages(room), {
        pages: [{ items, has_more: false }],
        pageParams: [undefined],
      });

    it("renames a DM's counterpart and title in the list, and refreshes the rest of it", () => {
      const dm = makeConversation({
        id: "r2",
        kind: "dm",
        title: "Atlas",
        counterpart: atlas,
      });
      const group = makeConversation({ id: "r1", title: "general" });
      qc.setQueryData<Conversation[]>(queryKeys.conversations, [dm, group]);
      change();
      const after = qc.getQueryData<Conversation[]>(queryKeys.conversations);
      expect(after?.[0]).toMatchObject({ title: "Atlas Prime", counterpart: renamed });
      expect(after?.[1]).toEqual(group);
      // Group previews and member names carry the old name: the list is fetched again.
      expect(qc.getQueryState(queryKeys.conversations)?.isInvalidated).toBe(true);
    });

    it("shows the new name, handle and avatar on every cached message of that author, and on no other", () => {
      seedMessages("r1", [byAtlas("r1", 1), byBob("r1", 2)]);
      seedMessages("r2", [byAtlas("r2", 1)]);
      change();
      expect(messages("r1")[0]).toMatchObject({ author_name: "Atlas Prime", author: renamed });
      expect(messages("r2")[0]).toMatchObject({ author_name: "Atlas Prime", author: renamed });
      expect(messages("r1")[1]).toEqual(byBob("r1", 2));
    });

    it("keeps the name an owner set for the AI in a room, and updates its handle and avatar there", () => {
      seedMessages("r1", [
        {
          ...byAtlas("r1", 1),
          author_name: "Release captain",
          author: { ...atlas, display_name: "Release captain" },
        },
      ]);
      qc.setQueryData<RoomMember[]>(queryKeys.roomMembers("r1"), [
        makeMember({
          id: "mb1",
          member_id: "a1",
          name: "Atlas",
          display_name: "Release captain",
          intelligence: atlas,
        }),
      ]);
      seedMessages("r2", [byAtlas("r2", 1)]);
      change();
      expect(messages("r1")[0]).toMatchObject({
        author_name: "Release captain",
        author: {
          display_name: "Release captain",
          handle: "atlas-prime",
          avatar_url: renamed.avatar_url,
        },
      });
      expect(messages("r2")[0]?.author_name).toBe("Atlas Prime");
    });

    it("removes a deleted avatar from the cached messages", () => {
      seedMessages("r1", [byAtlas("r1", 1)]);
      const { avatar_url: _gone, ...withoutAvatar } = renamed;
      change(withoutAvatar);
      expect(messages("r1")[0]?.author.avatar_url).toBeUndefined();
    });

    it("updates the member rows of that intelligence but not the per-room name", () => {
      qc.setQueryData<RoomMember[]>(queryKeys.roomMembers("r1"), [
        makeMember({
          id: "mb1",
          member_id: "a1",
          name: "Atlas",
          display_name: "Release captain",
          intelligence: atlas,
        }),
        makeMember({ id: "mb2", member_id: "a2", name: "Other", intelligence: undefined }),
      ]);
      change();
      const members = qc.getQueryData<RoomMember[]>(queryKeys.roomMembers("r1"));
      expect(members?.[0]).toMatchObject({
        name: "Atlas Prime",
        display_name: "Release captain",
        intelligence: renamed,
      });
      expect(members?.[1]?.name).toBe("Other");
    });

    it("updates the account's own profile in the shell data", () => {
      qc.setQueryData<Me>(queryKeys.me, makeMe());
      const mine = makeIntelligence({ display_name: "Ana S.", handle: "ana-s" });
      change(mine);
      expect(qc.getQueryData<Me>(queryKeys.me)).toMatchObject({
        display_name: "Ana S.",
        intelligence: mine,
      });
      // Someone else's change leaves the account alone.
      change();
      expect(qc.getQueryData<Me>(queryKeys.me)?.intelligence).toEqual(mine);
    });

    it("updates a cached profile page and the participants of cached DMs", () => {
      qc.setQueryData<IntelligenceProfile>(queryKeys.intelligence("a1"), {
        ...atlas,
        bio: "Keeps the deploys honest.",
        can_edit: true,
        apps: [],
        conversations: [],
        memberships: [],
        can_end_session: false,
      });
      const dm = makeRoom({
        id: "r2",
        kind: "dm",
        participants: [makeIntelligence(), atlas],
      });
      qc.setQueryData(queryKeys.room("r2"), dm);
      qc.setQueryData(queryKeys.rooms, [dm]);
      change();
      expect(qc.getQueryData<IntelligenceProfile>(queryKeys.intelligence("a1"))).toMatchObject({
        ...renamed,
        bio: "Keeps the deploys honest.",
        can_edit: true,
      });
      expect(qc.getQueryData<Room>(queryKeys.room("r2"))?.participants).toEqual([
        makeIntelligence(),
        renamed,
      ]);
      expect(qc.getQueryData<Room[]>(queryKeys.rooms)?.[0]?.participants[1]).toEqual(renamed);
    });

    it("clears the avatar of a cached profile when the change carries none", () => {
      qc.setQueryData<IntelligenceProfile>(queryKeys.intelligence("a1"), {
        ...atlas,
        bio: "",
        can_edit: true,
        apps: [],
        conversations: [],
        memberships: [],
        can_end_session: false,
      });
      const { avatar_url: _gone, ...withoutAvatar } = renamed;
      change(withoutAvatar);
      expect(
        qc.getQueryData<IntelligenceProfile>(queryKeys.intelligence("a1"))?.avatar_url,
      ).toBeUndefined();
    });
  });
  describe("contacts and operators (T42 part 2)", () => {
    const atlas = makeAI({ avatar_url: "/api/v1/intelligences/a1/avatar?v=1" });
    const renamed = makeAI({
      display_name: "Atlas Prime",
      handle: "atlas-prime",
      avatar_url: "/api/v1/intelligences/a1/avatar?v=2",
    });
    const change = (data: IntelligenceRef = renamed) =>
      handleLiveEvent(qc, actions(), { type: "intelligence.changed", data });
    const contacts = () => qc.getQueryData<Contacts>(queryKeys.contacts);
    const stale = (key: readonly unknown[]) => qc.getQueryState(key)?.isInvalidated;
    const notes = makeAI({ id: "a2", display_name: "Notes", handle: "notes" });

    it("renames an AI everywhere the cached contacts show it: every organization's group, the hosted AIs and as a host", () => {
      qc.setQueryData<Contacts>(
        queryKeys.contacts,
        makeContacts({
          org_ais: [
            makeOrgAIs({
              ais: [
                makeAIContact({ intelligence: notes }),
                makeAIContact({ intelligence: atlas }),
                makeAIContact({ intelligence: notes, owner: atlas }),
              ],
            }),
            makeOrgAIs({
              org: { id: "o2", name: "Globex", slug: "globex" },
              ais: [makeAIContact({ intelligence: atlas })],
            }),
          ],
          hosted: [makeAIContact({ intelligence: atlas, folder: "", folder_label: "" })],
        }),
      );
      change();
      const c = contacts();
      expect(c?.org_ais[0]?.ais[0]?.intelligence).toEqual(notes);
      expect(c?.org_ais[0]?.ais[1]?.intelligence).toEqual(renamed);
      expect(c?.org_ais[0]?.ais[2]?.owner).toEqual(renamed);
      expect(c?.org_ais[0]?.ais[2]?.intelligence).toEqual(notes);
      expect(c?.org_ais[1]?.ais[0]?.intelligence).toEqual(renamed);
      expect(c?.hosted[0]?.intelligence).toEqual(renamed);
      // A name change leaves where the AI lives and whether it is online alone.
      expect(c?.org_ais[0]?.ais[1]).toMatchObject({
        org_id: "o1",
        folder_label: "~/atlas",
        online: false,
      });
    });

    it("renames a user and the account itself in the cached contacts", () => {
      const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
      qc.setQueryData<Contacts>(queryKeys.contacts, makeContacts({ users: [bob] }));
      change(makeIntelligence({ id: "u2", display_name: "Bob L.", handle: "bob-l" }));
      expect(contacts()?.users[0]).toMatchObject({ display_name: "Bob L.", handle: "bob-l" });

      change(makeIntelligence({ display_name: "Ana S.", handle: "ana-s" }));
      expect(contacts()?.me).toMatchObject({ display_name: "Ana S.", handle: "ana-s" });
      expect(contacts()?.users[0]?.display_name).toBe("Bob L.");
    });

    it("drops the avatar of a contact when the change carries none", () => {
      qc.setQueryData<Contacts>(
        queryKeys.contacts,
        makeContacts({ users: [makeIntelligence({ ...atlas, role: "user" })] }),
      );
      const { avatar_url: _gone, ...withoutAvatar } = renamed;
      change(withoutAvatar);
      expect(contacts()?.users[0]?.avatar_url).toBeUndefined();
    });

    it("renames a person in the cached pages of its connector, and in no other person's row", () => {
      const lia = makePerson();
      const tom = makePerson({ id: "p2", display_name: "Tom Reis", handle: "tom" });
      const pages = (): PeoplePages => ({
        pages: [{ items: [lia, tom], has_more: false }],
        pageParams: [undefined],
      });
      qc.setQueryData(queryKeys.people("k1", ""), pages());
      qc.setQueryData(queryKeys.people("k1", "li"), pages());
      change(makePerson({ display_name: "Lia C.", handle: "lia-c" }));
      for (const key of [queryKeys.people("k1", ""), queryKeys.people("k1", "li")]) {
        const items = qc.getQueryData<PeoplePages>(key)?.pages[0]?.items;
        expect(items?.[0]).toMatchObject({ display_name: "Lia C.", handle: "lia-c" });
        expect(items?.[1]).toEqual(tom);
      }
    });

    it("leaves the contacts alone when nobody in them changed, and creates none that was never loaded", () => {
      const seeded = makeContacts({ users: [makeIntelligence({ id: "u2", handle: "bob" })] });
      qc.setQueryData<Contacts>(queryKeys.contacts, seeded);
      change();
      expect(contacts()).toBe(seeded);

      qc.clear();
      change();
      expect(contacts()).toBeUndefined();
    });

    it("shows the operator's new name on the cached connectors (ACC-01)", () => {
      const operated = makeConnector({ id: "k1", operator: atlas });
      const owned = makeConnector({ id: "k2", operator: makeMe().intelligence });
      qc.setQueryData<Connector[]>(queryKeys.connectors, [operated, owned]);
      qc.setQueryData<Connector>(queryKeys.connector("k1"), operated);
      qc.setQueryData<Connector>(queryKeys.connector("k2"), owned);
      change();
      const list = qc.getQueryData<Connector[]>(queryKeys.connectors);
      expect(list?.[0]?.operator).toEqual(renamed);
      expect(list?.[1]).toBe(owned);
      expect(qc.getQueryData<Connector>(queryKeys.connector("k1"))?.operator).toEqual(renamed);
      expect(qc.getQueryData<Connector>(queryKeys.connector("k2"))).toBe(owned);
    });

    it("refetches the contacts when an AI the cache does not know goes online: it was created by its first session", () => {
      qc.setQueryData<Contacts>(
        queryKeys.contacts,
        makeContacts({ org_ais: [makeOrgAIs({ ais: [makeAIContact()] })] }),
      );
      handleLiveEvent(qc, actions(), {
        type: "agent.presence",
        data: { agent_id: "a1", online: true },
      });
      expect(stale(queryKeys.contacts)).toBe(false);
      expect(useLiveStore.getState().presence.a1?.online).toBe(true);

      handleLiveEvent(qc, actions(), {
        type: "agent.presence",
        data: { agent_id: "a-new", online: true },
      });
      expect(stale(queryKeys.contacts)).toBe(true);
    });

    it("knows the hosted AIs and those of other organizations too, and does not refetch for them", () => {
      qc.setQueryData<Contacts>(
        queryKeys.contacts,
        makeContacts({
          hosted: [makeAIContact({ intelligence: notes, folder: "", folder_label: "" })],
          org_ais: [makeOrgAIs({ ais: [makeAIContact({ intelligence: makeAI({ id: "a3" }) })] })],
        }),
      );
      for (const id of ["a2", "a3"]) {
        handleLiveEvent(qc, actions(), {
          type: "agent.presence",
          data: { agent_id: id, online: false },
        });
      }
      expect(stale(queryKeys.contacts)).toBe(false);
    });

    it.each(["agent", "connector"])(
      "refreshes the contacts and their people lists when access to a %s changes",
      (kind) => {
        qc.setQueryData<Contacts>(queryKeys.contacts, makeContacts());
        qc.setQueryData<PeoplePages>(queryKeys.people("k1", ""), {
          pages: [{ items: [], has_more: false }],
          pageParams: [undefined],
        });
        handleLiveEvent(qc, actions(), {
          type: "access.changed",
          data: { resource_kind: kind, resource_id: "x1" },
        });
        expect(stale(queryKeys.contacts)).toBe(true);
        expect(stale(queryKeys.people("k1", ""))).toBe(true);
      },
    );

    it("leaves the contacts alone when access to a room changes", () => {
      qc.setQueryData<Contacts>(queryKeys.contacts, makeContacts());
      handleLiveEvent(qc, actions(), {
        type: "access.changed",
        data: { resource_kind: "room", resource_id: "r1" },
      });
      expect(stale(queryKeys.contacts)).toBe(false);
    });

    it("refreshes the contacts after a reconnect", async () => {
      qc.setQueryData<Contacts>(queryKeys.contacts, makeContacts());
      await catchUp(qc);
      expect(stale(queryKeys.contacts)).toBe(true);
    });
  });
});
