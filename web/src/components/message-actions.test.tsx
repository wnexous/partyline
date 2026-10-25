import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { canDeleteMessage, canEditMessage, MessageActions } from "@/components/message-actions";
import { type MessagePages, queryKeys } from "@/lib/api/queries";
import type { Message } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMessage } from "@/test/fixtures";
import { makeQueryClient, renderWithProviders } from "@/test/render";

/*
 * The actions of one message (EDT-10, DLT-09): "Edit" on the viewer's own text messages, "Delete" on their
 * own and, in a group they manage, on any message but a system notice. Choosing Delete asks first.
 */

const toast = vi.hoisted(() => Object.assign(vi.fn(), { error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

const ME = "u1";
const mine = makeMessage({ seq: 1, author_kind: "user", author_id: ME, text: "helo" });
const theirs = makeMessage({
  seq: 2,
  author_kind: "user",
  author_id: "u2",
  author: { id: "u2", kind: "human", role: "user", display_name: "Bob", handle: "bob" },
  text: "hello",
});

function renderActions(
  message: Message,
  over: { canModerate?: boolean; onEdit?: () => void } = {},
) {
  const onEdit = over.onEdit ?? vi.fn();
  const queryClient = makeQueryClient();
  const utils = renderWithProviders(
    <MessageActions
      message={message}
      meId={ME}
      canModerate={over.canModerate ?? false}
      onEdit={onEdit}
    />,
    queryClient,
  );
  return { ...utils, queryClient, onEdit };
}

const trigger = () => screen.queryByRole("button", { name: "Message actions" });
const open = async (user: ReturnType<typeof userEvent.setup>) => {
  const button = trigger();
  if (!button) throw new Error("no message actions button");
  await user.click(button);
};

beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  toast.mockClear();
  toast.error.mockClear();
});
afterEach(() => vi.unstubAllGlobals());

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

  it("the button is there for the author and the manager, not for anyone else", () => {
    mockApi({});
    const { unmount } = renderActions(mine);
    expect(trigger()).toBeInTheDocument();
    unmount();
    const other = renderActions(theirs);
    expect(trigger()).toBeNull();
    other.unmount();
    renderActions(theirs, { canModerate: true });
    expect(trigger()).toBeInTheDocument();
  });

  it("there is no button on a deleted message or on a system notice, whoever asks", () => {
    mockApi({});
    const first = renderActions(deleted, { canModerate: true });
    expect(trigger()).toBeNull();
    first.unmount();
    renderActions(system, { canModerate: true });
    expect(trigger()).toBeNull();
  });
});

describe("the menu (EDT-10, DLT-09)", () => {
  it("offers Edit and Delete on the viewer's own text message", async () => {
    mockApi({});
    const user = userEvent.setup();
    renderActions(mine);
    await open(user);
    expect(screen.getByRole("menuitem", { name: "Edit" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Delete" })).toBeInTheDocument();
  });

  it("offers Delete alone to the manager of a group on someone else's message", async () => {
    mockApi({});
    const user = userEvent.setup();
    renderActions(theirs, { canModerate: true });
    await open(user);
    expect(screen.getByRole("menuitem", { name: "Delete" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Edit" })).toBeNull();
  });

  it("offers Delete alone where there is no composer to edit in", async () => {
    mockApi({});
    const user = userEvent.setup();
    const queryClient = makeQueryClient();
    renderWithProviders(
      <MessageActions message={mine} meId={ME} canModerate={false} />,
      queryClient,
    );
    await open(user);
    expect(screen.getByRole("menuitem", { name: "Delete" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Edit" })).toBeNull();
  });

  it("Edit hands the message to the composer", async () => {
    mockApi({});
    const user = userEvent.setup();
    const { onEdit } = renderActions(mine);
    await open(user);
    await user.click(screen.getByRole("menuitem", { name: "Edit" }));
    // Once the menu has closed and let go of focus, so that the composer can keep it.
    await waitFor(() => expect(onEdit).toHaveBeenCalledWith(mine));
    expect(screen.queryByRole("menu")).toBeNull();
    expect(onEdit).toHaveBeenCalledTimes(1);
  });

  it("closing the menu without choosing Edit hands nothing to the composer", async () => {
    mockApi({});
    const user = userEvent.setup();
    const { onEdit } = renderActions(mine);
    await open(user);
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    expect(onEdit).not.toHaveBeenCalled();
    // Focus goes back to the button that opened the menu.
    expect(trigger()).toHaveFocus();
  });
});

describe("deleting (DLT-09)", () => {
  const deletedCopy = { ...mine, text: "", attachments: [], deleted_at: "2026-10-19T14:00:00Z" };

  async function chooseDelete(user: ReturnType<typeof userEvent.setup>) {
    await open(user);
    await user.click(screen.getByRole("menuitem", { name: "Delete" }));
    return await screen.findByRole("alertdialog");
  }

  it("asks first, and nothing is sent until the person confirms", async () => {
    const { calls } = mockApi({});
    const user = userEvent.setup();
    renderActions(mine);
    const dialog = await chooseDelete(user);
    expect(within(dialog).getByText("Delete this message?")).toBeInTheDocument();
    expect(within(dialog).getByText(/deleted for everyone/)).toBeInTheDocument();
    expect(within(dialog).getByText(/can’t be undone/)).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(0);

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(0);
  });

  it("Escape closes the question without deleting", async () => {
    const { calls } = mockApi({});
    const user = userEvent.setup();
    renderActions(mine);
    await chooseDelete(user);
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(0);
  });

  it("confirming deletes the message once and puts the placeholder in its place", async () => {
    const { calls } = mockApi({ "DELETE /rooms/r1/messages/m-r1-1": json(200, deletedCopy) });
    const user = userEvent.setup();
    const { queryClient } = renderActions(mine);
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
    renderActions(theirs, { canModerate: true });
    const dialog = await chooseDelete(user);
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1));
  });

  it("says when it fails, and the message stays", async () => {
    mockApi({ "DELETE /rooms/r1/messages/m-r1-1": apiError(403, "forbidden") });
    const user = userEvent.setup();
    const { queryClient } = renderActions(mine);
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
  });
});
