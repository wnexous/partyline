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
  makeContacts,
  makeConversation,
  makeIntelligence,
  makeMe,
  makeMember,
  makeMessage,
  makeRoom,
} from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { memberIn, viewerIn } from "@/test/orgs";
import { renderRoute } from "@/test/render";
import { sampleContacts } from "@/test/sample-contacts";

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
  useLiveStore.setState({ locale: "en", openRoomId: null, currentOrgId: null });
  window.localStorage.clear();
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

  it("previews a deleted last message as 'Message deleted', after the sender in a group (DLT-10)", async () => {
    api([
      makeConversation({
        id: "r1",
        title: "deploys",
        activity_at: today(11, 0),
        last_message: preview({
          seq: 4,
          author_name: "Bob Lima",
          text: "",
          deleted: true,
          created_at: today(11, 0),
        }),
      }),
      makeConversation({
        id: "r2",
        kind: "dm",
        title: "Atlas",
        counterpart: atlas,
        activity_at: today(10, 0),
        last_message: preview({
          seq: 8,
          author_id: "a1",
          text: "",
          deleted: true,
          created_at: today(10, 0),
        }),
      }),
    ]);
    renderRoute("/");
    const rows = within(await sidebar()).getAllByRole("link");
    expect(
      within(rows[0] as HTMLElement).getByText("Bob Lima: Message deleted"),
    ).toBeInTheDocument();
    expect(within(rows[1] as HTMLElement).getByText("Message deleted")).toBeInTheDocument();
    expect(within(rows[0] as HTMLElement).queryByText("No messages yet")).toBeNull();
  });

  it("follows a live edit of the last message, and its delete, in the preview (EDT-10, DLT-10)", async () => {
    api();
    const { queryClient } = renderRoute("/");
    const rows = within(await sidebar()).getAllByRole("link");
    expect(within(rows[1] as HTMLElement).getByText("Bob Lima: rolled back")).toBeInTheDocument();
    const sent = (over: Partial<Message>) => ({
      type: "room.message_updated" as const,
      data: makeMessage({
        room_id: "r1",
        seq: 4,
        author_id: "u2",
        author_name: "Bob Lima",
        author: bob,
        ...over,
      }),
    });

    act(() =>
      handleLiveEvent(
        queryClient,
        useLiveStore.getState(),
        sent({ text: "rolled forward", edited_at: today(11, 5) }),
      ),
    );
    expect(
      await within(rows[1] as HTMLElement).findByText("Bob Lima: rolled forward"),
    ).toBeInTheDocument();
    expect(within(rows[1] as HTMLElement).queryByText(/edited/)).toBeNull();

    act(() =>
      handleLiveEvent(
        queryClient,
        useLiveStore.getState(),
        sent({ text: "", deleted_at: today(11, 10) }),
      ),
    );
    expect(
      await within(rows[1] as HTMLElement).findByText("Bob Lima: Message deleted"),
    ).toBeInTheDocument();
    expect(within(rows[1] as HTMLElement).queryByText(/rolled/)).toBeNull();
  });

  it("says so when a conversation has no message yet", async () => {
    api();
    renderRoute("/");
    const rows = within(await sidebar()).getAllByRole("link");
    expect(within(rows[3] as HTMLElement).getByText("No messages yet")).toBeInTheDocument();
  });

  it("puts no kind badge next to a name: an AI, a platform person and a group read alike (PROF-09, AD-024)", async () => {
    api();
    renderRoute("/");
    const rows = within(await sidebar()).getAllByRole("link");
    const at = (index: number) => within(rows[index] as HTMLElement);
    const [ai, group, person] = [at(0), at(1), at(2)];
    expect(ai.getByText("Atlas")).toBeInTheDocument();
    expect(person.getByText("Lia Costa")).toBeInTheDocument();
    expect(group.getByText("deploys")).toBeInTheDocument();
    for (const row of [ai, group, person]) {
      expect(row.queryByTestId("kind-badge")).toBeNull();
      expect(row.queryByText(/^(AI|IA|WhatsApp)$/)).toBeNull();
    }
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

  it("gives an unread row the accent twice, its count and its time, and a read row neither", async () => {
    api();
    renderRoute("/");
    const rows = within(await sidebar()).getAllByRole("link");
    const unread = rows[0] as HTMLElement;
    const read = rows[1] as HTMLElement;
    expect(
      within(unread).getByText("3", { selector: "[aria-hidden=true]" }).parentElement,
    ).toHaveClass("bg-primary");
    expect(unread.querySelector("time")).toHaveClass("text-link");
    expect(read.querySelector("time")).toHaveClass("text-muted-foreground");
    expect(read.querySelector("time")).not.toHaveClass("text-link");
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
  it("offers to start a conversation or a group when there is none", async () => {
    api([]);
    renderRoute("/");
    expect(await screen.findByText("No conversations yet")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Conversations" })).toBeNull();
    // The header's buttons and the empty state's own: the same two actions twice.
    expect(screen.getAllByRole("button", { name: "New conversation" })).toHaveLength(2);
    expect(screen.getAllByRole("button", { name: "New group" })).toHaveLength(2);
    expect(screen.queryByRole("searchbox", { name: "Search conversations" })).toBeInTheDocument();
  });

  it("opens the dialog of the action chosen in the empty state", async () => {
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /conversations": json(200, { items: [] }),
      "GET /contacts": json(200, makeContacts()),
    });
    const user = userEvent.setup();
    renderRoute("/");
    const empty = (await screen.findByText("No conversations yet")).closest("section");
    await user.click(within(empty as HTMLElement).getByRole("button", { name: "New group" }));
    expect(await screen.findByRole("dialog", { name: "New group" })).toBeInTheDocument();
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

describe("search (LAY-03)", () => {
  // Names and members decide what matches; the order stays the one served.
  const conversations: Conversation[] = [
    makeConversation({
      id: "r1",
      title: "deploys",
      member_names: ["Ana Souza @ana", "Bob Lima @bob", "Atlas @atlas"],
    }),
    makeConversation({
      id: "r2",
      kind: "dm",
      title: "Atlas",
      counterpart: atlas,
      member_names: ["Atlas @atlas"],
    }),
    makeConversation({ id: "r3", title: "support-bridge", member_names: ["Cora Dias @cora"] }),
    makeConversation({
      id: "r4",
      kind: "dm",
      title: "João Pereira",
      counterpart: makeIntelligence({ id: "u9", display_name: "João Pereira", handle: "joao" }),
      member_names: ["João Pereira @joao"],
    }),
  ];
  const searchbox = () => screen.findByRole("searchbox", { name: "Search conversations" });
  const rows = (list: HTMLElement) =>
    within(list)
      .getAllByRole("link")
      .map((a) => a.getAttribute("href"));

  it("filters the list by conversation name as the user types", async () => {
    api(conversations);
    const user = userEvent.setup();
    renderRoute("/");
    const list = await sidebar();
    await user.type(await searchbox(), "SUPPORT");
    await waitFor(() => expect(rows(list)).toEqual(["/rooms/r3"]));
  });

  it("filters by the name or the handle of a member", async () => {
    api(conversations);
    const user = userEvent.setup();
    renderRoute("/");
    const list = await sidebar();
    const box = await searchbox();
    await user.type(box, "bob");
    await waitFor(() => expect(rows(list)).toEqual(["/rooms/r1"]));
    await user.clear(box);
    await user.type(box, "@atlas");
    await waitFor(() => expect(rows(list)).toEqual(["/rooms/r1", "/rooms/r2"]));
  });

  it("ignores case and accents", async () => {
    api(conversations);
    const user = userEvent.setup();
    renderRoute("/");
    const list = await sidebar();
    await user.type(await searchbox(), "JOAO");
    await waitFor(() => expect(rows(list)).toEqual(["/rooms/r4"]));
  });

  it("needs every word of the search, each found in the name or the members", async () => {
    api(conversations);
    const user = userEvent.setup();
    renderRoute("/");
    const list = await sidebar();
    const box = await searchbox();
    await user.type(box, "deploys atlas");
    await waitFor(() => expect(rows(list)).toEqual(["/rooms/r1"]));
    await user.type(box, " zed");
    expect(
      await screen.findByText("No conversation matches “deploys atlas zed”."),
    ).toBeInTheDocument();
  });

  it("brings the whole list back when the box is cleared, with its button or with Escape", async () => {
    api(conversations);
    const user = userEvent.setup();
    renderRoute("/");
    const list = await sidebar();
    const box = await searchbox();
    await user.type(box, "bob");
    await waitFor(() => expect(rows(list)).toHaveLength(1));

    await user.click(screen.getByRole("button", { name: "Clear search" }));
    await waitFor(() => expect(rows(list)).toHaveLength(4));
    expect(box).toHaveValue("");
    expect(box).toHaveFocus();

    await user.type(box, "bob");
    await waitFor(() => expect(rows(list)).toHaveLength(1));
    await user.keyboard("{Escape}");
    await waitFor(() => expect(rows(list)).toHaveLength(4));
    expect(box).toHaveValue("");
  });

  it("shows no clear button while the box is empty", async () => {
    api(conversations);
    renderRoute("/");
    await searchbox();
    expect(screen.queryByRole("button", { name: "Clear search" })).toBeNull();
  });

  it("says when nothing matches and offers the whole list back", async () => {
    api(conversations);
    const user = userEvent.setup();
    renderRoute("/");
    await sidebar();
    await user.type(await searchbox(), "zed");
    const status = await screen.findByText("No conversation matches “zed”.");
    expect(status.closest("[role=status]")).not.toBeNull();
    expect(screen.queryByRole("list", { name: "Conversations" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Show all conversations" }));
    expect(await sidebar()).toBeInTheDocument();
    expect(await searchbox()).toHaveValue("");
  });

  it("offers to look for the text among the contacts, so someone with no conversation yet can be reached", async () => {
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /conversations": json(200, { items: conversations }),
      "GET /contacts": json(200, sampleContacts),
    });
    const user = userEvent.setup();
    renderRoute("/");
    await sidebar();
    await user.type(await searchbox(), "lab");
    await user.click(await screen.findByRole("button", { name: "Find “lab” in contacts" }));
    const dialog = await screen.findByRole("dialog", { name: "New conversation" });
    expect(within(dialog).getByRole("combobox", { name: "Search contacts" })).toHaveValue("lab");
    expect(await within(dialog).findByTestId("contact-a3")).toHaveTextContent("Lab");
  });

  it("tells screen readers how many conversations match", async () => {
    api(conversations);
    const user = userEvent.setup();
    renderRoute("/");
    await sidebar();
    const box = await searchbox();
    expect(screen.queryByText(/conversations? found/)).toBeNull();
    await user.type(box, "atlas");
    expect(await screen.findByText("2 conversations found")).toBeInTheDocument();
    await user.clear(box);
    await user.type(box, "support");
    expect(await screen.findByText("1 conversation found")).toBeInTheDocument();
  });

  it("keeps filtering when a conversation changes: a hidden one stays hidden, a matching one moves up", async () => {
    api(conversations);
    const user = userEvent.setup();
    const { queryClient } = renderRoute("/");
    const list = await sidebar();
    await user.type(await searchbox(), "atlas");
    await waitFor(() => expect(rows(list)).toEqual(["/rooms/r1", "/rooms/r2"]));

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message",
        data: makeMessage({
          room_id: "r3",
          seq: 1,
          author_id: "u2",
          author: bob,
          created_at: today(14, 45),
        }),
      }),
    );
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message",
        data: makeMessage({
          room_id: "r2",
          seq: 1,
          author_kind: "agent",
          author_id: "a1",
          author: atlas,
          created_at: today(14, 50),
        }),
      }),
    );
    await waitFor(() => expect(rows(list)).toEqual(["/rooms/r2", "/rooms/r1"]));
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

/*
 * The list is the current organization's (ORG-02): its groups, and each DM filed under it. A DM is
 * filed under the organizations both sides belong to, or under the default one of the viewer when
 * they share none, so every conversation is in at least one list.
 */
describe("the current organization (ORG-02)", () => {
  const eve = makeIntelligence({ id: "u3", display_name: "Eve Dias", handle: "eve" });
  const items: Conversation[] = [
    makeConversation({ id: "g1", title: "acme crew", org_ids: ["o1"] }),
    makeConversation({ id: "g2", title: "globex crew", org_ids: ["o2"] }),
    // A colleague of Acme, someone of Globex only, and an AI of both.
    makeConversation({
      id: "d1",
      kind: "dm",
      title: "Bob Lima",
      counterpart: bob,
      org_ids: ["o1"],
    }),
    makeConversation({
      id: "d2",
      kind: "dm",
      title: "Eve Dias",
      counterpart: eve,
      org_ids: ["o2"],
    }),
    makeConversation({
      id: "d3",
      kind: "dm",
      title: "Atlas",
      counterpart: atlas,
      org_ids: ["o1", "o2"],
    }),
    makeConversation({ id: "g3", title: "legacy", org_ids: [] }),
  ];
  const acme = memberIn("o1", "member");
  const globex = memberIn("o2", "admin");
  const apiIn = (list: Conversation[] = items, ...memberships: ReturnType<typeof memberIn>[]) =>
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /conversations": json(200, { items: list }),
      ...viewerIn(...memberships),
    });
  const listed = async () => hrefs(await sidebar());
  const pick = (orgId: string) => act(() => useLiveStore.getState().setCurrentOrg(orgId));

  it("shows the groups of the current organization and hides those of another", async () => {
    apiIn(items, acme, globex);
    renderRoute("/");
    const rows = await listed();
    expect(rows).toContain("/rooms/g1");
    expect(rows).not.toContain("/rooms/g2");
  });

  it("shows a DM with a colleague of the organization and hides a DM with someone of another organization only", async () => {
    apiIn(items, acme, globex);
    renderRoute("/");
    const rows = await listed();
    expect(rows).toContain("/rooms/d1");
    expect(rows).not.toContain("/rooms/d2");
  });

  it("shows a DM both of two shared organizations hold in each of them", async () => {
    apiIn(items, acme, globex);
    renderRoute("/");
    expect(await listed()).toContain("/rooms/d3");
    pick("o2");
    await waitFor(async () => expect(await listed()).toContain("/rooms/d2"));
    expect(await listed()).toContain("/rooms/d3");
  });

  it("lists what is filed under the current organization, in the order served", async () => {
    apiIn(items, acme, globex);
    renderRoute("/");
    expect(await listed()).toEqual(["/rooms/g1", "/rooms/d1", "/rooms/d3", "/rooms/g3"]);
  });

  it("follows the organization the viewer picks", async () => {
    apiIn(items, acme, globex);
    renderRoute("/");
    await listed();
    pick("o2");
    await waitFor(async () =>
      expect(await listed()).toEqual(["/rooms/g2", "/rooms/d2", "/rooms/d3", "/rooms/g3"]),
    );
  });

  it("starts on the organization picked in an earlier visit", async () => {
    window.localStorage.setItem("pl.org", "o2");
    useLiveStore.setState({ currentOrgId: "o2" });
    apiIn(items, acme, globex);
    renderRoute("/");
    expect(await listed()).toEqual(["/rooms/g2", "/rooms/d2", "/rooms/d3", "/rooms/g3"]);
  });

  it("holds a conversation filed under no organization in every organization", async () => {
    apiIn(items, acme, globex);
    renderRoute("/");
    expect(await listed()).toContain("/rooms/g3");
    pick("o2");
    await waitFor(async () => expect(await listed()).toContain("/rooms/g2"));
    expect(await listed()).toContain("/rooms/g3");
  });

  it("lists every conversation while the viewer's organizations are unknown", async () => {
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /conversations": json(200, { items }),
      "GET /orgs": apiError(500, "internal", "boom"),
    });
    renderRoute("/");
    expect(await listed()).toEqual(items.map((c) => `/rooms/${c.id}`));
  });

  it("lists every conversation of a viewer who belongs to no organization", async () => {
    apiIn(items);
    renderRoute("/");
    expect(await listed()).toHaveLength(items.length);
  });

  it("waits for the organizations, so another organization's conversations never flash first", async () => {
    let release: () => void = () => {};
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /conversations": json(200, { items }),
      "GET /orgs": () =>
        new Promise<Response>((resolve) => {
          release = () => resolve(viewerIn(acme, globex)["GET /orgs"].clone());
        }),
    });
    renderRoute("/");
    await waitFor(() => expect(screen.queryByRole("list", { name: "Conversations" })).toBeNull());
    expect(screen.queryByRole("link", { name: /globex crew/ })).toBeNull();
    release();
    expect(await listed()).toEqual(["/rooms/g1", "/rooms/d1", "/rooms/d3", "/rooms/g3"]);
  });

  it("names the organization when only the others have conversations", async () => {
    apiIn(
      items.filter((c) => c.org_ids.includes("o2") && !c.org_ids.includes("o1")),
      acme,
      globex,
    );
    renderRoute("/");
    expect(await screen.findByText("No conversations in Acme yet")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Conversations" })).toBeNull();
    // The way to start one is still there, as in the list that is empty for good.
    expect(screen.getAllByRole("button", { name: "New group" })).toHaveLength(2);
  });

  it("keeps saying there are none when the viewer has no conversation at all", async () => {
    apiIn([], acme, globex);
    renderRoute("/");
    expect(await screen.findByText("No conversations yet")).toBeInTheDocument();
  });

  it("searches among the conversations of the organization only", async () => {
    apiIn(items, acme, globex);
    const user = userEvent.setup();
    renderRoute("/");
    await listed();
    await user.type(screen.getByRole("searchbox", { name: "Search conversations" }), "crew");
    await waitFor(async () => expect(await listed()).toEqual(["/rooms/g1"]));
  });

  it("brings a conversation in when a live frame files it under the organization", async () => {
    apiIn(items, acme, globex);
    const { queryClient } = renderRoute("/");
    expect(await listed()).not.toContain("/rooms/g2");
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.changed",
        data: makeRoom({ id: "g2", name: "globex crew", org_ids: ["o1", "o2"] }),
      }),
    );
    await waitFor(async () => expect(await listed()).toContain("/rooms/g2"));
  });

  it("speaks Portuguese when the organization has no conversation", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    mockApi({
      "GET /me": json(200, makeMe({ locale: "pt-BR" })),
      "GET /conversations": json(200, {
        items: items.filter((c) => c.org_ids.includes("o2") && !c.org_ids.includes("o1")),
      }),
      ...viewerIn(acme, globex),
    });
    renderRoute("/");
    expect(await screen.findByText("Nenhuma conversa em Acme ainda")).toBeInTheDocument();
  });
});
