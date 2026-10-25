import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Message, Room } from "@/lib/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeAI, makeIntelligence, makeMe, makeMessage, makeRoom } from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { renderRoute } from "@/test/render";

/*
 * Editing and deleting from a conversation (EDT-10, DLT-09, DLT-10): the menu of a message row, the composer
 * in edit mode and the confirmation of a delete, in the page.
 */

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));
const toast = vi.hoisted(() => Object.assign(vi.fn(), { error: vi.fn() }));
vi.mock("sonner", async (importOriginal) => ({ ...(await importOriginal<object>()), toast }));

const bobRef = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
const mine = makeMessage({ seq: 1, text: "helo wrld" });
const bobs = makeMessage({
  seq: 2,
  author_id: "u2",
  author_name: "Bob Lima",
  author: bobRef,
  text: "see you",
});

function page(items: Message[]) {
  return json(200, { items: [...items].reverse(), has_more: false });
}

function roomApi(
  room: Partial<Room> = {},
  messages: Message[] = [mine, bobs],
  extra: Parameters<typeof mockApi>[0] = {},
) {
  return mockApi({
    "GET /me": json(200, makeMe()),
    "GET /rooms/r1": json(200, makeRoom({ id: "r1", name: "deploys", ...room })),
    "GET /rooms/r1/members": json(200, { items: [] }),
    "GET /rooms/r1/messages": page(messages),
    ...extra,
  });
}

const row = (seq: number) => screen.getByTestId(`message-${seq}`);
const box = () => screen.getByRole("textbox", { name: "Message deploys" });

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  toast.mockClear();
  toast.error.mockClear();
});
afterEach(() => vi.unstubAllGlobals());

type User = ReturnType<typeof userEvent.setup>;

/** The "⋯" of a row, named after the author of its message. */
const moreOf = (seq: number) =>
  within(row(seq)).queryByRole("button", { name: /^Actions for the message from / });

/** Opens the menu of a row with its "⋯" and says what it offers. It is left open. */
async function openMenu(user: User, seq: number) {
  const more = moreOf(seq);
  if (!more) throw new Error(`message ${seq} has no ⋯`);
  await user.click(more);
  return within(await screen.findByRole("menu"))
    .getAllByRole("menuitem")
    .map((item) => item.textContent);
}

async function closeMenu(user: User) {
  await user.keyboard("{Escape}");
  await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
}

async function chooseEdit(user: User, seq: number) {
  await screen.findByTestId(`message-${seq}`);
  await openMenu(user, seq);
  await user.click(screen.getByRole("menuitem", { name: "Edit" }));
}

describe("editing from the menu (EDT-10)", () => {
  it("loads the message into the composer, saves it on Enter, and the row shows the new text, edited", async () => {
    const { calls } = roomApi({}, [mine, bobs], {
      "PATCH /rooms/r1/messages/m-r1-1": json(200, {
        ...mine,
        text: "hello world",
        edited_at: "2026-10-19T13:00:00Z",
      }),
    });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await chooseEdit(user, 1);

    expect(await screen.findByText("Editing message")).toBeInTheDocument();
    expect(box()).toHaveValue("helo wrld");
    await user.clear(box());
    await user.type(box(), "hello world{Enter}");

    await waitFor(() => expect(calls.filter((c) => c.method === "PATCH")).toHaveLength(1));
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ text: "hello world" });
    await waitFor(() => expect(within(row(1)).getByText("hello world")).toBeInTheDocument());
    expect(within(row(1)).getByText("edited")).toBeInTheDocument();
    expect(screen.queryByText("Editing message")).toBeNull();
    expect(box()).toHaveValue("");
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
  });

  it("Escape leaves editing and brings back the draft; nothing is sent", async () => {
    const { calls } = roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByText("helo wrld");
    await user.type(box(), "half a thought");
    await chooseEdit(user, 1);
    expect(box()).toHaveValue("helo wrld");
    await user.keyboard("{Escape}");

    await waitFor(() => expect(screen.queryByText("Editing message")).toBeNull());
    expect(box()).toHaveValue("half a thought");
    expect(calls.filter((c) => c.method === "PATCH" || c.method === "POST")).toHaveLength(0);
  });

  it("offers Edit on the viewer's own messages only, and not where there is no composer to edit in", async () => {
    roomApi({ my_level: "post" });
    const user = userEvent.setup();
    const first = renderRoute("/rooms/r1");
    await screen.findByText("see you");
    expect(await openMenu(user, 2)).toEqual(["Reply", "Copy text"]);
    await closeMenu(user);
    expect(await openMenu(user, 1)).toEqual(["Reply", "Copy text", "Edit", "Delete"]);
    first.unmount();

    // A viewer who cannot post has no composer, so no Reply and no Edit; what they wrote can still
    // be copied and deleted.
    roomApi({ my_level: "view" });
    renderRoute("/rooms/r1");
    await screen.findByText("see you");
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(await openMenu(user, 1)).toEqual(["Copy text", "Delete"]);
  });

  it("leaves editing when another conversation is opened", async () => {
    roomApi({}, [mine, bobs], {
      "GET /rooms/r2": json(200, makeRoom({ id: "r2", name: "support" })),
      "GET /rooms/r2/members": json(200, { items: [] }),
      "GET /rooms/r2/messages": page([makeMessage({ room_id: "r2", seq: 1, text: "elsewhere" })]),
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/rooms/r1");
    await chooseEdit(user, 1);
    expect(await screen.findByText("Editing message")).toBeInTheDocument();

    await act(() => router.navigate({ to: "/rooms/$roomId", params: { roomId: "r2" } }));
    await screen.findByText("elsewhere");
    expect(screen.queryByText("Editing message")).toBeNull();
    expect(screen.getByRole("textbox", { name: "Message support" })).toHaveValue("");
  });
});

describe("deleting from the menu (DLT-09, DLT-10)", () => {
  it("asks, then the row becomes 'Message deleted' in its place", async () => {
    const placeholder = { ...mine, text: "", deleted_at: "2026-10-19T14:00:00Z" };
    const { calls } = roomApi({}, [mine, bobs], {
      "DELETE /rooms/r1/messages/m-r1-1": json(200, placeholder),
    });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByTestId("message-1");
    await openMenu(user, 1);
    await user.click(screen.getByRole("menuitem", { name: "Delete" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Delete this message?" });
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(within(row(1)).getByText("Message deleted")).toBeInTheDocument());
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1);
    expect(screen.queryByText("helo wrld")).toBeNull();
    // A placeholder has nothing to reply to, copy or delete.
    expect(moreOf(1)).toBeNull();
    expect(screen.getAllByTestId(/^message-\d+$/).map((el) => el.dataset.testid)).toEqual([
      "message-1",
      "message-2",
    ]);
  });

  it("gives the manager of a group Delete on others' messages, and nobody does in a DM", async () => {
    roomApi({ my_level: "manage" });
    const user = userEvent.setup();
    const group = renderRoute("/rooms/r1");
    await screen.findByText("see you");
    expect(await openMenu(user, 2)).toEqual(["Reply", "Copy text", "Delete"]);
    group.unmount();

    // Nobody manages a DM: the level of a participant is post, and the other one's messages are theirs.
    const atlas = makeAI({ id: "ai-atlas", display_name: "Atlas", handle: "atlas" });
    roomApi({
      kind: "dm",
      my_level: "post",
      participants: [makeIntelligence({ id: "u1" }), atlas],
    });
    renderRoute("/rooms/r1");
    await screen.findByText("see you");
    expect(await openMenu(user, 2)).toEqual(["Reply", "Copy text"]);
    await closeMenu(user);
    expect(await openMenu(user, 1)).toEqual(["Reply", "Copy text", "Edit", "Delete"]);
  });

  it("shows a message another person deleted, live, as 'Message deleted'", async () => {
    roomApi();
    const { queryClient } = renderRoute("/rooms/r1");
    await screen.findByText("see you");
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message_updated",
        data: { ...bobs, text: "", deleted_at: "2026-10-19T14:00:00Z" },
      }),
    );
    await waitFor(() => expect(within(row(2)).getByText("Message deleted")).toBeInTheDocument());
    expect(screen.queryByText("see you")).toBeNull();
    expect(within(row(2)).getByText("Bob Lima")).toBeInTheDocument();
  });
});
