import type { QueryClient } from "@tanstack/react-query";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { queryKeys } from "@/lib/api/queries";
import type { Conversation, Message } from "@/lib/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeConversation,
  makeIntelligence,
  makeMe,
  makeMember,
  makeMessage,
  makeRoom,
} from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * The messenger's sidebar list (LAY-01, LAY-02, DM-06): what each row shows, the order, the
 * unread badge, and how a live message changes them.
 */

const atlas = makeIntelligence({
  id: "a1",
  kind: "ai",
  role: "ai",
  display_name: "Atlas",
  handle: "atlas",
});
const lia = makeIntelligence({
  id: "p1",
  role: "person",
  platform: "whatsapp",
  display_name: "Lia Costa",
  handle: "lia",
});
const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });

// Local-time constructors: the clock on a row follows the viewer's time zone.
const today = (h: number, m: number) => new Date(2026, 9, 21, h, m).toISOString();
const NOW = new Date(2026, 9, 21, 15, 0);

function preview(over: Partial<NonNullable<Conversation["last_message"]>> = {}) {
  return {
    seq: 1,
    author_id: "u2",
    author_name: "",
    text: "hello",
    created_at: today(12, 30),
    ...over,
  };
}

const served: Conversation[] = [
  makeConversation({
    id: "r2",
    kind: "dm",
    title: "Atlas",
    counterpart: atlas,
    unread: 3,
    activity_at: today(12, 30),
    last_message: preview({ seq: 8, author_id: "a1", text: "Deploy finished" }),
  }),
  makeConversation({
    id: "r1",
    title: "deploys",
    activity_at: today(11, 0),
    last_message: preview({
      seq: 4,
      author_name: "Bob Lima",
      text: "rolled back",
      created_at: today(11, 0),
    }),
  }),
  makeConversation({
    id: "r3",
    kind: "dm",
    title: "Lia Costa",
    counterpart: lia,
    unread: 1,
    activity_at: today(10, 0),
    last_message: preview({
      seq: 2,
      author_id: "p1",
      text: "see you at 3",
      created_at: today(10, 0),
    }),
  }),
  makeConversation({ id: "r4", title: "ops", activity_at: today(9, 0) }),
];

function api(items: Conversation[] = served) {
  return mockApi({
    "GET /me": json(200, makeMe()),
    "GET /conversations": json(200, { items }),
  });
}

const sidebar = () => screen.findByRole("list", { name: "Conversations" });
const hrefs = (list: HTMLElement) =>
  within(list)
    .getAllByRole("link")
    .map((a) => a.getAttribute("href"));

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en", openRoomId: null });
  vi.useFakeTimers({ toFake: ["Date"], now: NOW });
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("conversation list (LAY-01, DM-06)", () => {
  it("lists direct messages and groups together, in the order served, each opening its conversation", async () => {
    api();
    renderRoute("/");
    expect(hrefs(await sidebar())).toEqual(["/rooms/r2", "/rooms/r1", "/rooms/r3", "/rooms/r4"]);
  });

  it("shows avatar, name, preview and time on each row, with the sender only in groups", async () => {
    api();
    renderRoute("/");
    const rows = within(await sidebar()).getAllByRole("link");

    const dm = within(rows[0] as HTMLElement);
    expect(dm.getByTestId("intelligence-avatar")).toHaveTextContent("A");
    expect(dm.getByText("Atlas")).toBeInTheDocument();
    expect(dm.getByText("Deploy finished")).toBeInTheDocument();
    expect(dm.getByText("12:30 PM")).toBeInTheDocument();

    const group = within(rows[1] as HTMLElement);
    expect(group.getByTestId("intelligence-avatar")).toHaveTextContent("D");
    expect(group.getByText("deploys")).toBeInTheDocument();
    expect(group.getByText("Bob Lima: rolled back")).toBeInTheDocument();
    expect(group.getByText("11:00 AM")).toHaveAttribute("datetime", today(11, 0));
  });

  it("says so when a conversation has no message yet", async () => {
    api();
    renderRoute("/");
    const rows = within(await sidebar()).getAllByRole("link");
    expect(within(rows[3] as HTMLElement).getByText("No messages yet")).toBeInTheDocument();
  });

  it("marks an AI and a platform person next to their names (PROF-09)", async () => {
    api();
    renderRoute("/");
    const rows = within(await sidebar()).getAllByRole("link");
    expect(within(rows[0] as HTMLElement).getByText("AI")).toBeInTheDocument();
    expect(within(rows[2] as HTMLElement).getByText("WhatsApp")).toBeInTheDocument();
    // A group has no kind to show.
    expect(within(rows[1] as HTMLElement).queryByTestId("kind-badge")).toBeNull();
  });

  it("shows the unread count of each conversation, and none for a read one (DM-06)", async () => {
    api();
    renderRoute("/");
    const rows = within(await sidebar()).getAllByRole("link");
    expect(within(rows[0] as HTMLElement).getByText("3 unread messages")).toBeInTheDocument();
    expect(within(rows[2] as HTMLElement).getByText("1 unread message")).toBeInTheDocument();
    expect(within(rows[1] as HTMLElement).queryByText(/unread/)).toBeNull();
    expect(within(rows[3] as HTMLElement).queryByText(/unread/)).toBeNull();
  });

  it("caps a long count at 99+ on screen and keeps the full count for screen readers", async () => {
    api([
      makeConversation({ id: "r1", title: "busy", unread: 120 }),
      makeConversation({ id: "r2", title: "full", unread: 100 }),
      makeConversation({ id: "r3", title: "almost", unread: 99 }),
    ]);
    renderRoute("/");
    const [busy, full, almost] = within(await sidebar())
      .getAllByRole("link")
      .map((a) => within(a));
    expect(busy?.getByText("99+")).toBeInTheDocument();
    expect(busy?.getByText("120 unread messages")).toBeInTheDocument();
    expect(full?.getByText("99+")).toBeInTheDocument();
    expect(almost?.getByText("99", { selector: "[aria-hidden=true]" })).toBeInTheDocument();
    expect(almost?.getByText("99 unread messages")).toBeInTheDocument();
  });

  it("shows the clock in 24 hours in Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    mockApi({
      "GET /me": json(200, makeMe({ locale: "pt-BR" })),
      "GET /conversations": json(200, { items: served }),
    });
    renderRoute("/");
    const rows = within(await screen.findByRole("list", { name: "Conversas" })).getAllByRole(
      "link",
    );
    expect(within(rows[0] as HTMLElement).getByText("12:30")).toBeInTheDocument();
    expect(within(rows[0] as HTMLElement).getByText("3 mensagens não lidas")).toBeInTheDocument();
  });

  it("marks the open conversation as the current page", async () => {
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /conversations": json(200, { items: served }),
      "GET /rooms/r1": json(200, makeRoom({ id: "r1", name: "deploys" })),
      "GET /rooms/r1/members": json(200, { items: [] }),
      "GET /rooms/r1/messages": json(200, { items: [], has_more: false }),
    });
    renderRoute("/rooms/r1");
    const rows = within(await sidebar()).getAllByRole("link");
    expect(rows.map((r) => r.getAttribute("aria-current"))).toEqual([null, "page", null, null]);
  });
});

describe("empty and failed list", () => {
  it("offers to create the first room when there is no conversation", async () => {
    api([]);
    renderRoute("/");
    expect(await screen.findByText("No conversations yet")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Conversations" })).toBeNull();
    expect(screen.getAllByRole("button", { name: "New room" }).length).toBeGreaterThan(0);
  });

  it("says the list failed and tries again on request", async () => {
    let attempts = 0;
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /conversations": () => {
        attempts += 1;
        return attempts === 1 ? apiError(500, "internal", "boom") : json(200, { items: served });
      },
    });
    const user = userEvent.setup();
    renderRoute("/");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Couldn’t load your conversations.");
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(hrefs(await sidebar())).toHaveLength(4);
  });
});

describe("a refresh that fails", () => {
  it("keeps the list on screen instead of replacing it with an error", async () => {
    let attempts = 0;
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /conversations": () => {
        attempts += 1;
        return attempts === 1 ? json(200, { items: served }) : apiError(500, "internal", "boom");
      },
    });
    const { queryClient } = renderRoute("/");
    const list = await sidebar();
    await act(() => queryClient.invalidateQueries({ queryKey: queryKeys.conversations }));
    await waitFor(() => expect(attempts).toBe(2));
    await waitFor(() =>
      expect(queryClient.getQueryState(queryKeys.conversations)?.status).toBe("error"),
    );
    expect(screen.queryByRole("alert")).toBeNull();
    expect(hrefs(list)).toHaveLength(4);
  });
});

describe("live messages (LAY-02)", () => {
  const live = (queryClient: QueryClient, data: Message) =>
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), { type: "room.message", data }),
    );
  const fromBob = (roomId: string, seq: number, over: Partial<Message> = {}) =>
    makeMessage({
      room_id: roomId,
      seq,
      author_id: "u2",
      author_name: "Bob Lima",
      author: bob,
      created_at: today(14, 45),
      ...over,
    });

  it("moves the conversation to the top with its new preview and an unread badge", async () => {
    api();
    const { queryClient } = renderRoute("/");
    const list = await sidebar();

    live(queryClient, fromBob("r4", 1, { text: "anyone around?" }));

    await waitFor(() =>
      expect(hrefs(list)).toEqual(["/rooms/r4", "/rooms/r2", "/rooms/r1", "/rooms/r3"]),
    );
    const top = within(within(list).getAllByRole("link")[0] as HTMLElement);
    expect(top.getByText("ops")).toBeInTheDocument();
    expect(top.getByText("Bob Lima: anyone around?")).toBeInTheDocument();
    expect(top.getByText("1 unread message")).toBeInTheDocument();
    expect(top.getByText("02:45 PM")).toBeInTheDocument();
  });

  it("adds to the count of a conversation that already has unread messages", async () => {
    api();
    const { queryClient } = renderRoute("/");
    const list = await sidebar();
    live(
      queryClient,
      fromBob("r2", 9, { author_kind: "agent", author_id: "a1", author: atlas, text: "one more" }),
    );
    const row = within(within(list).getAllByRole("link")[0] as HTMLElement);
    await waitFor(() => expect(row.getByText("4 unread messages")).toBeInTheDocument());
    expect(row.getByText("one more")).toBeInTheDocument();
  });

  it("does not badge the user's own messages", async () => {
    api();
    const { queryClient } = renderRoute("/");
    const list = await sidebar();
    live(
      queryClient,
      makeMessage({
        room_id: "r4",
        seq: 1,
        author_name: "Ana Souza",
        text: "note to self",
        created_at: today(14, 45),
      }),
    );
    await waitFor(() => expect(hrefs(list)[0]).toBe("/rooms/r4"));
    const top = within(within(list).getAllByRole("link")[0] as HTMLElement);
    expect(top.queryByText(/unread/)).toBeNull();
    expect(top.getByText("Ana Souza: note to self")).toBeInTheDocument();
  });

  it("does not badge a message in the conversation that is open, and still badges the others", async () => {
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /conversations": json(200, { items: served }),
      "GET /rooms/r4": json(200, makeRoom({ id: "r4", name: "ops" })),
      "GET /rooms/r4/members": json(200, { items: [makeMember()] }),
      "GET /rooms/r4/messages": json(200, { items: [], has_more: false }),
    });
    const { queryClient } = renderRoute("/rooms/r4");
    const list = await sidebar();
    await waitFor(() => expect(useLiveStore.getState().openRoomId).toBe("r4"));

    live(queryClient, fromBob("r4", 5, { created_at: today(14, 50) }));
    live(queryClient, fromBob("r1", 5, { created_at: today(14, 51) }));

    const row = (href: string) =>
      within(list)
        .getAllByRole("link")
        .find((a) => a.getAttribute("href") === href) as HTMLElement;
    await waitFor(() =>
      expect(within(row("/rooms/r1")).getByText("1 unread message")).toBeInTheDocument(),
    );
    expect(within(row("/rooms/r4")).queryByText(/unread/)).toBeNull();
    // The open conversation moved up all the same, just behind the newer one.
    expect(hrefs(list).slice(0, 2)).toEqual(["/rooms/r1", "/rooms/r4"]);
  });
});
