import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { canDeleteMessage, canEditMessage } from "@/components/message-actions";
import {
  type MessageCapabilities,
  MessageMenu,
  type MessageMenuTarget,
} from "@/components/message-menu";
import { type MessagePages, queryKeys } from "@/lib/api/queries";
import type { Message } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMessage } from "@/test/fixtures";
import { touchDevice } from "@/test/gestures";
import { makeQueryClient, renderWithProviders } from "@/test/render";

/*
 * The actions of one message that change it (EDT-10, DLT-09): "Edit" on the viewer's own text messages,
 * "Delete" on their own and, in a group they manage, on any message but a system notice. They are two of the
 * actions of the message's menu (components/message-menu.tsx), as a menu with a mouse and as a sheet on a
 * touch device. Choosing Delete asks first.
 */

// Only `toast` is ours; the rest of the module stays.
const toast = vi.hoisted(() => Object.assign(vi.fn(), { error: vi.fn() }));
vi.mock("sonner", async (importOriginal) => ({
  ...(await importOriginal<typeof import("sonner")>()),
  toast,
}));

const ME = "u1";
const mine = makeMessage({ seq: 1, author_kind: "user", author_id: ME, text: "helo" });
const theirs = makeMessage({
  seq: 2,
  author_kind: "user",
  author_id: "u2",
  author: { id: "u2", kind: "human", role: "user", display_name: "Bob", handle: "bob" },
  text: "hello",
});

const caps = (over: Partial<MessageCapabilities> = {}): MessageCapabilities => ({
  meId: ME,
  canReply: true,
  canModerate: false,
  canEdit: true,
  frozen: false,
  ...over,
});

/**
 * The menu of `message`, open, as the timeline holds it: closing it lets the target go. `opener` is the
 * button that opened it, where focus goes back to.
 */
function renderMenu(
  message: Message,
  over: Partial<MessageCapabilities> & { touch?: boolean } = {},
) {
  const opener = document.createElement("button");
  opener.textContent = "opener";
  opener.dataset.testOpener = "";
  document.body.append(opener);
  const onEdit = vi.fn();
  const onReply = vi.fn();
  const queryClient = makeQueryClient();
  function Harness() {
    const [target, setTarget] = useState<MessageMenuTarget | null>({
      message,
      at: { x: 40, y: 40 },
      opener,
    });
    return (
      <MessageMenu
        target={target}
        onClose={() => setTarget(null)}
        touch={over.touch ?? false}
        capabilities={caps(over)}
        onReply={onReply}
        onEdit={onEdit}
      />
    );
  }
  const utils = renderWithProviders(<Harness />, queryClient);
  return { ...utils, queryClient, onEdit, onReply, opener };
}

beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  toast.mockClear();
  toast.error.mockClear();
});
afterEach(() => {
  vi.unstubAllGlobals();
  for (const opener of document.querySelectorAll("[data-test-opener]")) opener.remove();
});

describe("who may edit and delete (EDT-02, DLT-02)", () => {
  const chat = makeMessage({ seq: 3, author_kind: "chat", author_id: "c1", author_user_id: ME });
  const agent = makeMessage({ seq: 4, author_kind: "agent", author_id: "a1" });
  const system = makeMessage({ seq: 5, author_kind: "system", author_id: undefined });
  const reaction = makeMessage({
    seq: 6,
    author_kind: "user",
    author_id: ME,
    kind: "reaction",
    text: "👍",
  });
  const deleted = makeMessage({
    seq: 7,
    author_kind: "user",
    author_id: ME,
    text: "",
    deleted_at: "2026-10-19T14:00:00Z",
  });
  const fileOnly = makeMessage({
    seq: 8,
    author_kind: "user",
    author_id: ME,
    text: "",
    attachments: [{ id: "f1", name: "a.txt", mime: "text/plain", size: 3, status: "stored" }],
  });

  it("the author edits and deletes a text message, a file with no caption included", () => {
    for (const m of [mine, fileOnly]) {
      expect(canEditMessage(m, ME)).toBe(true);
      expect(canDeleteMessage(m, ME, false)).toBe(true);
    }
  });

  it("nobody edits what someone else wrote, a manager and an AI's message included", () => {
    for (const m of [theirs, chat, agent, system]) expect(canEditMessage(m, ME)).toBe(false);
  });

  it("a person who only posts deletes nothing of others; the manager of the group deletes all but system notices", () => {
    for (const m of [theirs, chat, agent, system])
      expect(canDeleteMessage(m, ME, false)).toBe(false);
    for (const m of [theirs, chat, agent]) expect(canDeleteMessage(m, ME, true)).toBe(true);
    expect(canDeleteMessage(system, ME, true)).toBe(false);
  });

  it("a reaction is deleted and not edited, and a deleted message is neither", () => {
    expect(canEditMessage(reaction, ME)).toBe(false);
    expect(canDeleteMessage(reaction, ME, false)).toBe(true);
    expect(canEditMessage(deleted, ME)).toBe(false);
    expect(canDeleteMessage(deleted, ME, true)).toBe(false);
  });
});

describe("the menu (EDT-10, DLT-09)", () => {
  it("offers Edit and Delete on the viewer's own text message, after Reply and Copy text", async () => {
    mockApi({});
    renderMenu(mine);
    const menu = await screen.findByRole("menu");
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((i) => i.textContent),
    ).toEqual(["Reply", "Copy text", "Edit", "Delete"]);
  });

  it("offers Delete alone, of the two, to the manager of a group on someone else's message", async () => {
    mockApi({});
    renderMenu(theirs, { canModerate: true });
    const menu = await screen.findByRole("menu");
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((i) => i.textContent),
    ).toEqual(["Reply", "Copy text", "Delete"]);
  });

  it("offers neither to a person who only posts, on someone else's message", async () => {
    mockApi({});
    renderMenu(theirs);
    const menu = await screen.findByRole("menu");
    expect(screen.queryByRole("menuitem", { name: "Edit" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: "Delete" })).toBeNull();
    expect(within(menu).getAllByRole("menuitem")).toHaveLength(2);
  });

  it("offers Delete alone where there is no composer to edit in", async () => {
    mockApi({});
    renderMenu(mine, { canEdit: false });
    expect(await screen.findByRole("menuitem", { name: "Delete" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Edit" })).toBeNull();
  });

  it("offers Copy text alone in a conversation that is history", async () => {
    mockApi({});
    renderMenu(mine, { frozen: true, canModerate: true });
    const menu = await screen.findByRole("menu");
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((i) => i.textContent),
    ).toEqual(["Copy text"]);
  });

  it("puts Delete last, set apart and in the destructive colour", async () => {
    mockApi({});
    renderMenu(mine);
    const del = await screen.findByRole("menuitem", { name: "Delete" });
    expect(del).toHaveAttribute("data-variant", "destructive");
    expect(screen.getByRole("menuitem", { name: "Edit" })).toHaveAttribute(
      "data-variant",
      "default",
    );
    expect(screen.getAllByRole("separator")).toHaveLength(1);
    expect(del.previousElementSibling).toBe(screen.getByRole("separator"));
  });

  it("Edit hands the message to the composer", async () => {
    mockApi({});
    const user = userEvent.setup();
    const { onEdit, opener } = renderMenu(mine);
    await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
    // Once the menu has closed and let go of focus, so that the composer can keep it.
    await waitFor(() => expect(onEdit).toHaveBeenCalledWith(mine));
    expect(screen.queryByRole("menu")).toBeNull();
    expect(onEdit).toHaveBeenCalledTimes(1);
    // The composer takes focus, not the button that opened the menu.
    expect(opener).not.toHaveFocus();
  });

  it("closing the menu without choosing Edit hands nothing to the composer", async () => {
    mockApi({});
    const user = userEvent.setup();
    const { onEdit, opener } = renderMenu(mine);
    await screen.findByRole("menu");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    expect(onEdit).not.toHaveBeenCalled();
    // Focus goes back to the button that opened the menu.
    await waitFor(() => expect(opener).toHaveFocus());
  });

  it("on a touch device they are buttons of the sheet, and Edit hands over the message too", async () => {
    touchDevice();
    mockApi({});
    const user = userEvent.setup();
    const { onEdit } = renderMenu(mine, { touch: true });
    const sheet = await screen.findByRole("dialog", { name: "Ana" });
    expect(within(sheet).getByRole("button", { name: "Reply" })).toBeInTheDocument();
    expect(within(sheet).getByRole("button", { name: "Copy text" })).toBeInTheDocument();
    expect(within(sheet).getByRole("button", { name: "Delete" })).toBeInTheDocument();
    await user.click(within(sheet).getByRole("button", { name: "Edit" }));
    await waitFor(() => expect(onEdit).toHaveBeenCalledWith(mine));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Ana" })).toBeNull());
  });
});

describe("deleting (DLT-09)", () => {
  const deletedCopy = { ...mine, text: "", attachments: [], deleted_at: "2026-10-19T14:00:00Z" };

  async function chooseDelete(user: ReturnType<typeof userEvent.setup>) {
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    return await screen.findByRole("alertdialog");
  }

  it("asks first, and nothing is sent until the person confirms", async () => {
    const { calls } = mockApi({});
    const user = userEvent.setup();
    renderMenu(mine);
    const dialog = await chooseDelete(user);
    expect(within(dialog).getByText("Delete this message?")).toBeInTheDocument();
    expect(within(dialog).getByText(/deleted for everyone/)).toBeInTheDocument();
    expect(within(dialog).getByText(/can’t be undone/)).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(0);

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(0);
  });

  it("the question follows the menu: it is closed when the question is asked", async () => {
    mockApi({});
    const user = userEvent.setup();
    renderMenu(mine);
    await chooseDelete(user);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("Escape closes the question without deleting, and focus goes back to the opener", async () => {
    const { calls } = mockApi({});
    const user = userEvent.setup();
    const { opener } = renderMenu(mine);
    await chooseDelete(user);
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(0);
    await waitFor(() => expect(opener).toHaveFocus());
  });

  it("confirming deletes the message once and puts the placeholder in its place", async () => {
    const { calls } = mockApi({ "DELETE /rooms/r1/messages/m-r1-1": json(200, deletedCopy) });
    const user = userEvent.setup();
    const { queryClient } = renderMenu(mine);
    queryClient.setQueryData<MessagePages>(queryKeys.roomMessages("r1"), {
      pages: [{ items: [mine, theirs], has_more: false }],
      pageParams: [undefined],
    });
    const dialog = await chooseDelete(user);
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    const items = queryClient.getQueryData<MessagePages>(queryKeys.roomMessages("r1"))?.pages[0]
      ?.items;
    expect(items?.map((m) => m.seq)).toEqual([1, 2]);
    expect(items?.[0]).toEqual(deletedCopy);
    expect(items?.[1]).toEqual(theirs);
  });

  it("a manager deletes someone else's message the same way", async () => {
    const { calls } = mockApi({
      "DELETE /rooms/r1/messages/m-r1-2": json(200, {
        ...theirs,
        text: "",
        deleted_at: "2026-10-19T14:00:00Z",
      }),
    });
    const user = userEvent.setup();
    renderMenu(theirs, { canModerate: true });
    const dialog = await chooseDelete(user);
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1));
  });

  it("on a touch device the question is the same, asked over the sheet's place", async () => {
    touchDevice();
    const { calls } = mockApi({ "DELETE /rooms/r1/messages/m-r1-1": json(200, deletedCopy) });
    const user = userEvent.setup();
    renderMenu(mine, { touch: true });
    const sheet = await screen.findByRole("dialog", { name: "Ana" });
    await user.click(within(sheet).getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("Delete this message?")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(0);
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1));
  });

  it("says when it fails, and the message stays", async () => {
    mockApi({ "DELETE /rooms/r1/messages/m-r1-1": apiError(403, "forbidden") });
    const user = userEvent.setup();
    const { queryClient } = renderMenu(mine);
    queryClient.setQueryData<MessagePages>(queryKeys.roomMessages("r1"), {
      pages: [{ items: [mine], has_more: false }],
      pageParams: [undefined],
    });
    const dialog = await chooseDelete(user);
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn’t delete the message."));
    expect(
      queryClient.getQueryData<MessagePages>(queryKeys.roomMessages("r1"))?.pages[0]?.items[0],
    ).toEqual(mine);
    // The question stays, so that the person can try again or give up.
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
  });
});
