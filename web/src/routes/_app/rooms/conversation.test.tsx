import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { IntelligenceProfile, Message, Room } from "@/lib/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import {
  makeConversation,
  makeIntelligence,
  makeMe,
  makeMember,
  makeMessage,
  makeRoom,
} from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { stubViewport } from "@/test/media";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * The conversation page (DM-07, DM-02, GRP-02, PROF-09): its header, the participants it lists,
 * what the timeline shows of each author, and how opening it marks it read.
 */

const me = makeMe();
const ana = me.intelligence;
const atlas = makeIntelligence({
  id: "a1",
  kind: "ai",
  role: "ai",
  display_name: "Atlas",
  handle: "atlas",
});
const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });

const atlasProfile: IntelligenceProfile = {
  ...atlas,
  bio: "",
  can_edit: false,
  apps: [{ kind: "claude", label: "Claude Code" }],
  conversations: [],
  ai: {
    owner: ana,
    machine: "devbox",
    machine_label: "devbox",
    folder: "/home/dev/atlas",
    folder_label: "~/atlas",
    app: "claude",
    online: true,
    my_level: "manage",
  },
};

const dm: Room = makeRoom({
  id: "d1",
  kind: "dm",
  name: "",
  participants: [ana, atlas],
  my_level: "post",
  member_count: 2,
});
const group: Room = makeRoom({
  id: "g1",
  name: "release-crew",
  my_level: "manage",
  member_count: 3,
});

const dmMembers = [
  makeMember({ id: "mb-u1", kind: "user", member_id: "u1", name: "Ana Souza", intelligence: ana }),
  makeMember({ id: "mb-a1", kind: "agent", member_id: "a1", name: "Atlas", intelligence: atlas }),
];
const groupMembers = [
  makeMember({
    id: "mb-a1",
    kind: "agent",
    member_id: "a1",
    name: "Atlas",
    intelligence: atlas,
    owner: "ana",
    online: true,
  }),
  makeMember({
    id: "mb-u2",
    kind: "user",
    member_id: "u2",
    name: "Bob Lima",
    intelligence: bob,
    online: undefined,
    owner: undefined,
  }),
  makeMember({
    id: "mb-c1",
    kind: "chat",
    member_id: "c1",
    name: "Ops on call",
    online: undefined,
    owner: undefined,
    connector_kind: "telegram",
    chat_kind: "group",
  }),
];

const from = (seq: number, over: Partial<Message> = {}) =>
  makeMessage({
    room_id: "d1",
    seq,
    author_kind: "agent",
    author_id: "a1",
    author_name: "Atlas",
    author: atlas,
    text: `message ${seq}`,
    created_at: "2026-10-19T12:00:00Z",
    ...over,
  });

function api(extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, me),
    "GET /conversations": json(200, {
      items: [
        makeConversation({ id: "d1", kind: "dm", title: "Atlas", counterpart: atlas, unread: 3 }),
        makeConversation({ id: "g1", title: "release-crew", unread: 1 }),
      ],
    }),
    "GET /rooms/d1": json(200, dm),
    "GET /rooms/d1/members": json(200, { items: dmMembers }),
    "GET /rooms/d1/messages": json(200, {
      items: [from(1), from(2), from(3), from(4), from(5)],
      has_more: false,
    }),
    "GET /intelligences/a1": json(200, atlasProfile),
    "GET /rooms/g1": json(200, group),
    "GET /rooms/g1/members": json(200, { items: groupMembers }),
    "GET /rooms/g1/messages": json(200, { items: [], has_more: false }),
    "PUT /rooms/d1/read": json(200, { room_id: "d1", seq: 5, unread: 0 }),
    "PUT /rooms/g1/read": json(200, { room_id: "g1", seq: 0, unread: 0 }),
    ...extra,
  });
}

const header = () => screen.findByTestId("conversation-header");
const reads = (calls: { method: string; path: string; body: unknown }[], room = "d1") =>
  calls.filter((c) => c.method === "PUT" && c.path === `/rooms/${room}/read`).map((c) => c.body);

function setVisibility(state: "visible" | "hidden") {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
  act(() => {
    document.dispatchEvent(new Event("visibilitychange"));
  });
}

stubLayout();
beforeEach(() => useLiveStore.setState({ locale: "en", openRoomId: null, presence: {} }));
afterEach(() => {
  vi.unstubAllGlobals();
  // Drops the shadowing property so the prototype's getter answers again.
  delete (document as unknown as Record<string, unknown>).visibilityState;
});

describe("conversation header", () => {
  it("titles a DM with its counterpart: avatar, name, AI badge, @handle, app and presence", async () => {
    api();
    renderRoute("/rooms/d1");
    const bar = within(await header());
    expect(await bar.findByRole("heading", { name: "Atlas", level: 1 })).toBeInTheDocument();
    expect(bar.getByTestId("intelligence-avatar")).toHaveTextContent("A");
    expect(bar.getByText("AI")).toBeInTheDocument();
    expect(bar.getByText("@atlas")).toBeInTheDocument();
    expect(await bar.findByText("Claude Code")).toBeInTheDocument();
    expect(bar.getByText("Online")).toBeInTheDocument();
  });

  it("follows the counterpart's live presence", async () => {
    api();
    const { queryClient } = renderRoute("/rooms/d1");
    const bar = within(await header());
    await bar.findByText("Online");
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.presence",
        data: { agent_id: "a1", online: false },
      }),
    );
    expect(await bar.findByText("Offline")).toBeInTheDocument();
  });

  it("names both participants of a DM the user only views through an AI", async () => {
    api({
      "GET /rooms/d1": json(
        200,
        makeRoom({
          id: "d1",
          kind: "dm",
          name: "",
          participants: [atlas, bob],
          my_level: "view",
          member_count: 2,
        }),
      ),
    });
    renderRoute("/rooms/d1");
    const bar = within(await header());
    expect(
      await bar.findByRole("heading", { name: "Atlas AI and Bob Lima", level: 1 }),
    ).toBeInTheDocument();
  });

  it("puts each participant's kind badge next to its name when the user only views the DM (PROF-09)", async () => {
    const dana = makeIntelligence({
      id: "p1",
      role: "person",
      platform: "whatsapp",
      display_name: "Dana",
      handle: "dana",
    });
    api({
      "GET /rooms/d1": json(
        200,
        makeRoom({
          id: "d1",
          kind: "dm",
          name: "",
          participants: [atlas, dana],
          my_level: "view",
          member_count: 2,
        }),
      ),
    });
    renderRoute("/rooms/d1");
    const title = within(await header()).getByRole("heading", { level: 1 });
    const [first, second] = await within(title).findAllByTestId("intelligence-name");
    expect(within(first as HTMLElement).getByText("Atlas")).toBeInTheDocument();
    expect(within(first as HTMLElement).getByTestId("kind-badge")).toHaveTextContent("AI");
    expect(within(second as HTMLElement).getByText("Dana")).toBeInTheDocument();
    expect(within(second as HTMLElement).getByTestId("kind-badge")).toHaveTextContent("WhatsApp");
  });

  it("titles a group by its name with its visibility and no AI details", async () => {
    api();
    renderRoute("/rooms/g1");
    const bar = within(await header());
    expect(await bar.findByRole("heading", { name: "release-crew", level: 1 })).toBeInTheDocument();
    expect(bar.getByText("Private")).toBeInTheDocument();
    expect(bar.getByTestId("intelligence-avatar")).toHaveTextContent("R");
    expect(bar.queryByText("Online")).toBeNull();
  });

  it("has a back button to the list on a phone", async () => {
    stubViewport(360);
    api();
    renderRoute("/rooms/d1");
    const bar = within(await header());
    expect(await bar.findByRole("link", { name: "Back to conversations" })).toHaveAttribute(
      "href",
      "/",
    );
  });
});

describe("participants (DM-02, GRP-02)", () => {
  it("shows the two participants of a DM, with kind badges and handles, and no way to change them", async () => {
    api();
    renderRoute("/rooms/d1");
    const panel = await screen.findByRole("complementary", { name: "Participants" });
    const you = within(await within(panel).findByTestId("participant-u1"));
    expect(you.getByText("Ana Souza")).toBeInTheDocument();
    expect(you.getByText("You")).toBeInTheDocument();
    expect(you.getByText("@ana")).toBeInTheDocument();
    const ai = within(within(panel).getByTestId("participant-a1"));
    expect(ai.getByText("Atlas")).toBeInTheDocument();
    expect(ai.getByText("AI")).toBeInTheDocument();
    expect(ai.getByText("@atlas")).toBeInTheDocument();
    expect(within(panel).getAllByTestId(/^participant-/)).toHaveLength(2);

    // Nobody adds, removes or configures anyone in a DM, and nobody shares or edits it.
    for (const name of [
      /Add members/,
      /Remove/,
      /Outbound/,
      /Settings for/,
      "Share",
      "Room settings",
    ]) {
      expect(screen.queryByRole("button", { name })).toBeNull();
    }
    expect(screen.queryByRole("switch")).toBeNull();
  });

  it("keeps a DM's participants fixed whatever level the caller holds", async () => {
    api({ "GET /rooms/d1": json(200, { ...dm, my_level: "manage" }) });
    renderRoute("/rooms/d1");
    const panel = await screen.findByRole("complementary", { name: "Participants" });
    await within(panel).findByTestId("participant-a1");
    for (const name of [/Add members/, /Remove/, "Share", "Room settings"]) {
      expect(screen.queryByRole("button", { name })).toBeNull();
    }
  });

  it("lists the AIs and users of a group with avatar, badge and handle, and its chats as before", async () => {
    api();
    renderRoute("/rooms/g1");
    const panel = await screen.findByRole("complementary", { name: "Members" });
    const ai = within(await within(panel).findByTestId("member-mb-a1"));
    expect(ai.getByTestId("intelligence-avatar")).toHaveTextContent("A");
    expect(ai.getByText("Atlas")).toBeInTheDocument();
    expect(ai.getByText("AI")).toBeInTheDocument();
    expect(ai.getByText("@atlas")).toBeInTheDocument();
    expect(ai.getByText("Online")).toBeInTheDocument();
    expect(ai.getByText("by @ana")).toBeInTheDocument();

    const user = within(within(panel).getByTestId("member-mb-u2"));
    expect(user.getByTestId("intelligence-avatar")).toHaveTextContent("BL");
    expect(user.getByText("Bob Lima")).toBeInTheDocument();
    expect(user.getByText("@bob")).toBeInTheDocument();
    expect(within(panel).getByRole("region", { name: "Users" })).toContainElement(
      user.getByText("Bob Lima"),
    );

    const chat = within(within(panel).getByTestId("member-mb-c1"));
    expect(chat.getByText("Ops on call")).toBeInTheDocument();
    expect(chat.getByText(/Telegram/)).toBeInTheDocument();
  });

  it("keeps the member controls of a group for its manager", async () => {
    api();
    renderRoute("/rooms/g1");
    expect(await screen.findByRole("button", { name: "Add members" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove Atlas" })).toBeInTheDocument();
  });
});

describe("authors in the timeline (PROF-09)", () => {
  it("shows an AI's avatar, kind badge and, on focus, its @handle", async () => {
    api();
    renderRoute("/rooms/d1");
    const row = within(await screen.findByTestId("message-1"));
    expect(row.getByTestId("intelligence-avatar")).toHaveTextContent("A");
    expect(row.getByText("AI")).toBeInTheDocument();
    expect(row.queryByText("@atlas")).toBeNull();
    // The author's name is a link to its profile (PDM-03); the handle stays in its tooltip.
    act(() => row.getByRole("link", { name: "Atlas" }).focus());
    expect(await screen.findByRole("tooltip")).toHaveTextContent("@atlas");
  });
});

describe("marking read (DM-07)", () => {
  it("reads the conversation up to its newest message when it opens, and the badge goes", async () => {
    const { calls } = api();
    renderRoute("/rooms/d1");
    await waitFor(() => expect(reads(calls)).toEqual([{ seq: 5 }]));
    const list = await screen.findByRole("list", { name: "Conversations" });
    const row = within(list).getByRole("link", { name: /Atlas/ });
    await waitFor(() => expect(within(row).queryByText(/unread/)).toBeNull());
    // Only this conversation was read.
    expect(
      within(within(list).getByRole("link", { name: /release-crew/ })).getByText(
        "1 unread message",
      ),
    ).toBeInTheDocument();
    expect(reads(calls, "g1")).toEqual([]);
  });

  it("reads each new message while the conversation is on screen, and each one once", async () => {
    const { calls } = api();
    const { queryClient } = renderRoute("/rooms/d1");
    await waitFor(() => expect(reads(calls)).toEqual([{ seq: 5 }]));

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message",
        data: from(6),
      }),
    );
    await waitFor(() => expect(reads(calls)).toEqual([{ seq: 5 }, { seq: 6 }]));

    // The same message again, and a refresh of the members, ask for nothing more.
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message",
        data: from(6),
      }),
    );
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.members",
        data: { room_id: "d1" },
      }),
    );
    await screen.findByText("message 6");
    expect(reads(calls)).toEqual([{ seq: 5 }, { seq: 6 }]);
  });

  it("reads nothing while the tab is hidden, and what arrived meanwhile once it shows again", async () => {
    const { calls } = api();
    const { queryClient } = renderRoute("/rooms/d1");
    await waitFor(() => expect(reads(calls)).toEqual([{ seq: 5 }]));

    setVisibility("hidden");
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message",
        data: from(6),
      }),
    );
    await screen.findByText("message 6");
    expect(reads(calls)).toEqual([{ seq: 5 }]);

    setVisibility("visible");
    await waitFor(() => expect(reads(calls)).toEqual([{ seq: 5 }, { seq: 6 }]));
  });

  it("does not read again when the tab shows and nothing new arrived", async () => {
    const { calls } = api();
    renderRoute("/rooms/d1");
    await waitFor(() => expect(reads(calls)).toEqual([{ seq: 5 }]));
    setVisibility("hidden");
    setVisibility("visible");
    await screen.findByText("message 5");
    expect(reads(calls)).toEqual([{ seq: 5 }]);
  });

  it("opens a tab that is already hidden unread, and reads on showing", async () => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
    const { calls } = api();
    renderRoute("/rooms/d1");
    await screen.findByText("message 5");
    expect(reads(calls)).toEqual([]);
    setVisibility("visible");
    await waitFor(() => expect(reads(calls)).toEqual([{ seq: 5 }]));
  });

  it("has nothing to read in a conversation without messages", async () => {
    const { calls } = api();
    renderRoute("/rooms/g1");
    await screen.findByRole("heading", { name: "No messages yet" });
    expect(reads(calls, "g1")).toEqual([]);
  });

  it("reads the next conversation when the user moves to it", async () => {
    const { calls } = api({
      "GET /rooms/g1/messages": json(200, {
        items: [from(1, { room_id: "g1" }), from(2, { room_id: "g1" })],
        has_more: false,
      }),
      "PUT /rooms/g1/read": json(200, { room_id: "g1", seq: 2, unread: 0 }),
    });
    const user = userEvent.setup();
    renderRoute("/rooms/d1");
    await waitFor(() => expect(reads(calls)).toEqual([{ seq: 5 }]));
    await user.click(
      within(await screen.findByRole("list", { name: "Conversations" })).getByRole("link", {
        name: /release-crew/,
      }),
    );
    await waitFor(() => expect(reads(calls, "g1")).toEqual([{ seq: 2 }]));
  });
});
