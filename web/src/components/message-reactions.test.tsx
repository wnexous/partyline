import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import { MessageTimeline } from "@/components/message-timeline";
import type { Message, Reaction } from "@/shared/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { QUICK_REACTIONS } from "@/lib/reactions";
import { json, mockApi } from "@/test/fetch";
import { makeMessage } from "@/test/fixtures";
import { finger, HOLD, longPress, mouseDevice, touchDevice, wait } from "@/test/gestures";
import { stubLayout } from "@/test/layout";
import { renderWithRouter } from "@/test/render";

expect.extend(matchers);

// Only `toast` is ours; the rest of the module stays.
const toast = vi.hoisted(() => Object.assign(vi.fn(), { error: vi.fn() }));
vi.mock("sonner", async (importOriginal) => ({
  ...(await importOriginal<typeof import("sonner")>()),
  toast,
}));

/*
 * Reacting to a message in the conversation (RCT-01, RCT-02, RCT-08): the chips under it, the bar of the sheet
 * and of the menu, the toolbar of the row under a mouse, and the picker behind "More".
 */

const AXE = { rules: { "color-contrast": { enabled: false } } };
const ME = "u1";
const THEM = "u2";

const page = (items: Message[]) => json(200, { items: [...items].reverse(), has_more: false });
const row = (seq: number) => screen.getByTestId(`message-${seq}`);
const onReply = vi.fn();

/** Ana's message, which the viewer (u1) did not write: it offers Reply, Copy text and the reactions. */
const ana = (seq: number, over: Partial<Message> = {}) =>
  makeMessage({
    seq,
    text: `ana says ${seq}`,
    author_id: THEM,
    author: { id: THEM, kind: "human", role: "user", display_name: "Ana", handle: "ana" },
    ...over,
  });
const thumbs = (...by: string[]): Reaction[] => [{ emoji: "👍", by }];

function renderTimeline(
  items: Message[],
  props: { react?: boolean; frozen?: boolean } = {},
  routes: Parameters<typeof mockApi>[0] = {},
) {
  const api = mockApi({ "GET /rooms/r1/messages": page(items), ...routes });
  const utils = renderWithRouter(
    <MessageTimeline
      roomId="r1"
      meId={ME}
      members={[]}
      onReply={onReply}
      canReact={props.react ?? true}
      frozen={props.frozen}
    />,
  );
  return { ...utils, ...api };
}
type Calls = ReturnType<typeof mockApi>["calls"];
const posts = (calls: Calls) => calls.filter((c) => c.method === "POST");
const deletes = (calls: Calls) => calls.filter((c) => c.method === "DELETE");
const react = (emoji: string) => screen.getByRole("button", { name: `React with ${emoji}` });

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  onReply.mockClear();
  toast.mockClear();
  toast.error.mockClear();
});
afterEach(() => vi.unstubAllGlobals());

describe("the chips under a message (RCT-01)", () => {
  beforeEach(mouseDevice);

  it("show each emoji with its count, under the text and the files, in the order first used", async () => {
    renderTimeline([
      ana(1, {
        reactions: [
          { emoji: "🎉", by: [THEM] },
          { emoji: "👍", by: [THEM, ME] },
        ],
      }),
    ]);
    await screen.findByText("ana says 1");
    const list = within(row(1)).getByRole("list", { name: "Reactions" });
    expect(
      within(list)
        .getAllByRole("listitem")
        .map((i) => i.textContent?.replace(/\s+/g, "")),
    ).toEqual(["🎉1", "👍2"]);
    // Under the text.
    const text = within(row(1)).getByText("ana says 1");
    expect(text.compareDocumentPosition(list) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("are not there for a message with no reactions", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    expect(within(row(1)).queryByRole("list", { name: "Reactions" })).toBeNull();
  });

  it("mark the viewer's own and add or take it back on a tap, before the answer comes", async () => {
    let release: (r: Response) => void = () => {};
    const waiting = new Promise<Response>((resolve) => {
      release = resolve;
    });
    const { calls } = renderTimeline(
      [ana(1, { reactions: thumbs(THEM) })],
      {},
      {
        "POST /rooms/r1/messages/m-r1-1/reactions": () => waiting,
      },
    );
    await screen.findByText("ana says 1");
    const chip = within(row(1)).getByRole("button", { name: "👍 1 reaction" });
    expect(chip).toHaveAttribute("aria-pressed", "false");
    await userEvent.click(chip);
    // The reaction shows before the server has answered.
    const mine = await within(row(1)).findByRole("button", {
      name: "👍 2 reactions, including yours",
    });
    expect(mine).toHaveAttribute("aria-pressed", "true");
    expect(posts(calls)).toHaveLength(1);
    expect(posts(calls)[0]).toMatchObject({
      path: "/rooms/r1/messages/m-r1-1/reactions",
      body: { emoji: "👍" },
    });
    await act(async () => release(json(200, { items: thumbs(THEM, ME) })));
  });

  it("take the viewer's reaction back on a tap", async () => {
    const { calls } = renderTimeline(
      [ana(1, { reactions: thumbs(THEM, ME) })],
      {},
      {
        "DELETE /rooms/r1/messages/m-r1-1/reactions/%F0%9F%91%8D": json(200, {
          items: thumbs(THEM),
        }),
      },
    );
    await screen.findByText("ana says 1");
    await userEvent.click(
      within(row(1)).getByRole("button", { name: "👍 2 reactions, including yours" }),
    );
    expect(await within(row(1)).findByRole("button", { name: "👍 1 reaction" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    await waitFor(() => expect(deletes(calls)).toHaveLength(1));
  });

  it("drop the emoji when the last reaction is taken back", async () => {
    renderTimeline(
      [ana(1, { reactions: thumbs(ME) })],
      {},
      {
        "DELETE /rooms/r1/messages/m-r1-1/reactions/%F0%9F%91%8D": json(200, { items: [] }),
      },
    );
    await screen.findByText("ana says 1");
    await userEvent.click(within(row(1)).getByRole("button", { name: "👍 1 reaction, yours" }));
    await waitFor(() =>
      expect(within(row(1)).queryByRole("list", { name: "Reactions" })).toBeNull(),
    );
  });

  it("undo themselves when the request fails, and say so", async () => {
    renderTimeline(
      [ana(1, { reactions: thumbs(THEM) })],
      {},
      {
        "POST /rooms/r1/messages/m-r1-1/reactions": json(500, {
          error: { code: "internal", message: "boom" },
        }),
      },
    );
    await screen.findByText("ana says 1");
    await userEvent.click(within(row(1)).getByRole("button", { name: "👍 1 reaction" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn’t save your reaction."));
    expect(within(row(1)).getByRole("button", { name: "👍 1 reaction" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
  });

  it("follow the live frame of someone else's reaction, in place", async () => {
    const { queryClient } = renderTimeline([ana(1), ana(2)]);
    await screen.findByText("ana says 2");
    const { handleLiveEvent } = await import("@/lib/live/handlers");
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.reactions",
        data: { room_id: "r1", message_id: "m-r1-1", reactions: thumbs(THEM) },
      }),
    );
    expect(
      await within(row(1)).findByRole("button", { name: "👍 1 reaction" }),
    ).toBeInTheDocument();
    expect(within(row(2)).queryByRole("list", { name: "Reactions" })).toBeNull();
    expect(screen.getAllByTestId(/^message-\d+$/).map((el) => el.dataset.testid)).toEqual([
      "message-1",
      "message-2",
    ]);
  });

  it("are shown, with nothing to press, to a viewer who may not react", async () => {
    renderTimeline([ana(1, { reactions: thumbs(THEM, ME) })], { react: false });
    await screen.findByText("ana says 1");
    const list = within(row(1)).getByRole("list", { name: "Reactions" });
    expect(within(list).queryByRole("button")).toBeNull();
    expect(within(list).getByText("👍 2 reactions, including yours")).toBeInTheDocument();
  });

  it("are shown, with nothing to press, in a conversation that is history", async () => {
    renderTimeline([ana(1, { reactions: thumbs(THEM) })], { frozen: true });
    await screen.findByText("ana says 1");
    const list = within(row(1)).getByRole("list", { name: "Reactions" });
    expect(within(list).queryByRole("button")).toBeNull();
  });

  it("are not shown on a deleted message, which has none", async () => {
    renderTimeline([
      ana(1, { text: "", deleted_at: "2026-10-19T14:00:00Z", reactions: thumbs(THEM) }),
    ]);
    await screen.findByText("Message deleted");
    expect(within(row(1)).queryByRole("list", { name: "Reactions" })).toBeNull();
  });

  it("are in the viewer's language", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    renderTimeline([ana(1, { reactions: thumbs(THEM, ME) })]);
    await screen.findByText("ana says 1");
    expect(
      within(row(1)).getByRole("button", { name: "👍 2 reações, incluindo a sua" }),
    ).toBeInTheDocument();
  });

  it("have no axe violations", async () => {
    renderTimeline([ana(1, { reactions: [...thumbs(THEM, ME), { emoji: "🎉", by: [THEM] }] })]);
    await screen.findByText("ana says 1");
    expect(await axe(row(1), AXE)).toHaveNoViolations();
  });
});

describe("the bar of the sheet (RCT-02)", () => {
  beforeEach(touchDevice);

  async function openSheet(over: Parameters<typeof renderTimeline>[1] = {}, items = [ana(1)]) {
    const rendered = renderTimeline(items, over, {
      "POST /rooms/r1/messages/m-r1-1/reactions": json(200, { items: thumbs(ME) }),
      "DELETE /rooms/r1/messages/m-r1-1/reactions/%F0%9F%91%8D": json(200, { items: [] }),
    });
    await screen.findByText("ana says 1");
    await longPress(row(1));
    const sheet = await screen.findByRole("dialog", { name: "Ana" });
    return { ...rendered, sheet };
  }

  it("starts with the six quick emoji and a button for more, before the actions", async () => {
    const { sheet } = await openSheet();
    const bar = within(sheet).getByRole("group", { name: "Quick reactions" });
    expect(
      within(bar)
        .getAllByRole("button")
        .map((b) => b.getAttribute("aria-label")),
    ).toEqual([...QUICK_REACTIONS.map((e) => `React with ${e}`), "More reactions"]);
    const reply = within(sheet).getByRole("button", { name: "Reply" });
    expect(bar.compareDocumentPosition(reply) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(sheet).toHaveAccessibleDescription("ana says 1");
  });

  it("is not there where the viewer may not react", async () => {
    const { sheet } = await openSheet({ react: false });
    expect(within(sheet).queryByRole("group", { name: "Quick reactions" })).toBeNull();
    expect(within(sheet).getByRole("button", { name: "Reply" })).toBeInTheDocument();
  });

  it("is not there in a conversation that is history", async () => {
    const { sheet } = await openSheet({ frozen: true });
    expect(within(sheet).queryByRole("group", { name: "Quick reactions" })).toBeNull();
  });

  it("marks the emoji the viewer used", async () => {
    const { sheet } = await openSheet({}, [
      ana(1, {
        reactions: [
          { emoji: "❤️", by: [ME] },
          { emoji: "👍", by: [THEM] },
        ],
      }),
    ]);
    expect(within(sheet).getByRole("button", { name: "React with ❤️" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(within(sheet).getByRole("button", { name: "React with 👍" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
  });

  it("reacts with the emoji tapped, closes, and the chip is under the message", async () => {
    const { calls } = await openSheet();
    await userEvent.click(react("👍"));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(posts(calls)).toHaveLength(1);
    expect(posts(calls)[0]?.body).toEqual({ emoji: "👍" });
    expect(
      await within(row(1)).findByRole("button", { name: "👍 1 reaction, yours" }),
    ).toBeInTheDocument();
  });

  it("takes the reaction back when the emoji tapped is the viewer's own", async () => {
    const { calls } = await openSheet({}, [ana(1, { reactions: thumbs(ME) })]);
    await userEvent.click(react("👍"));
    await waitFor(() => expect(deletes(calls)).toHaveLength(1));
    await waitFor(() =>
      expect(within(row(1)).queryByRole("list", { name: "Reactions" })).toBeNull(),
    );
  });

  it("opens the rest in place of the actions, and shuts it again", async () => {
    const { sheet } = await openSheet();
    const more = within(sheet).getByRole("button", { name: "More reactions" });
    await userEvent.click(more);
    expect(more).toHaveAttribute("aria-expanded", "true");
    const grid = within(sheet).getByRole("group", { name: "Pick a reaction" });
    expect(within(grid).getAllByRole("button").length).toBeGreaterThan(40);
    expect(within(sheet).queryByRole("button", { name: "Reply" })).toBeNull();
    // The bar stays, so the six are still at hand.
    expect(within(sheet).getByRole("group", { name: "Quick reactions" })).toBeInTheDocument();

    await userEvent.click(more);
    expect(more).toHaveAttribute("aria-expanded", "false");
    expect(within(sheet).queryByRole("group", { name: "Pick a reaction" })).toBeNull();
    expect(within(sheet).getByRole("button", { name: "Reply" })).toBeInTheDocument();
  });

  it("reacts with an emoji of the rest and closes", async () => {
    const { calls, sheet } = await openSheet();
    await userEvent.click(within(sheet).getByRole("button", { name: "More reactions" }));
    await userEvent.click(within(sheet).getByRole("button", { name: "React with 🚀" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(posts(calls)[0]?.body).toEqual({ emoji: "🚀" });
  });

  it("shows the rest closed the next time", async () => {
    const { sheet } = await openSheet();
    await userEvent.click(within(sheet).getByRole("button", { name: "More reactions" }));
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await longPress(row(1));
    const again = await screen.findByRole("dialog", { name: "Ana" });
    expect(within(again).getByRole("button", { name: "More reactions" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(within(again).getByRole("button", { name: "Reply" })).toBeInTheDocument();
  });

  it("has no axe violations, shut or open", async () => {
    const { sheet } = await openSheet({}, [ana(1, { reactions: thumbs(ME) })]);
    expect(await axe(sheet, AXE)).toHaveNoViolations();
    await userEvent.click(within(sheet).getByRole("button", { name: "More reactions" }));
    expect(await axe(sheet, AXE)).toHaveNoViolations();
  });

  it("is in Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    const { sheet } = await openSheet();
    expect(within(sheet).getByRole("group", { name: "Reações rápidas" })).toBeInTheDocument();
    expect(within(sheet).getByRole("button", { name: "Reagir com 👍" })).toBeInTheDocument();
  });

  it("does not react to a tap on a chip that ends a long press", async () => {
    const { calls } = renderTimeline([ana(1, { reactions: thumbs(THEM) })]);
    await screen.findByText("ana says 1");
    const chip = within(row(1)).getByRole("button", { name: "👍 1 reaction" });
    await longPress(chip);
    await screen.findByRole("dialog", { name: "Ana" });
    expect(posts(calls)).toHaveLength(0);
    expect(deletes(calls)).toHaveLength(0);
  });

  it("has no toolbar on the row, grouped or not: a finger has the sheet", async () => {
    renderTimeline([ana(1), ana(2)]);
    await screen.findByText("ana says 2");
    expect(within(row(1)).queryByRole("toolbar")).toBeNull();
    // The second message of the same author shows its time in the corner; the toolbar is not beside it.
    expect(within(row(2)).queryByRole("toolbar")).toBeNull();
    expect(screen.queryByRole("toolbar")).toBeNull();
  });
});

describe("the menu of a mouse (RCT-02)", () => {
  beforeEach(mouseDevice);

  async function openMenu(over: Parameters<typeof renderTimeline>[1] = {}, items = [ana(1)]) {
    const rendered = renderTimeline(items, over, {
      "POST /rooms/r1/messages/m-r1-1/reactions": json(200, { items: thumbs(ME) }),
      "DELETE /rooms/r1/messages/m-r1-1/reactions/%F0%9F%91%8D": json(200, { items: [] }),
    });
    await screen.findByText("ana says 1");
    fireEvent.contextMenu(row(1), { clientX: 300, clientY: 200 });
    const menu = await screen.findByRole("menu", { name: "Actions for the message from Ana" });
    return { ...rendered, menu };
  }

  it("starts with the six quick emoji and an item for more, then a rule, then the actions", async () => {
    const { menu } = await openMenu();
    const group = within(menu).getByRole("group", { name: "Quick reactions" });
    expect(
      within(group)
        .getAllByRole("menuitemcheckbox")
        .map((i) => i.getAttribute("aria-label")),
    ).toEqual(QUICK_REACTIONS.map((e) => `React with ${e}`));
    expect(within(group).getByRole("menuitem", { name: "More reactions" })).toBeInTheDocument();
    const reply = within(menu).getByRole("menuitem", { name: "Reply" });
    const rule = within(menu).getAllByRole("separator")[0] as HTMLElement;
    expect(group.compareDocumentPosition(rule) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(rule.compareDocumentPosition(reply) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("is not there where the viewer may not react", async () => {
    const { menu } = await openMenu({ react: false });
    expect(within(menu).queryByRole("group", { name: "Quick reactions" })).toBeNull();
    expect(within(menu).queryByRole("separator")).toBeNull();
  });

  it("checks the emoji the viewer used", async () => {
    const { menu } = await openMenu({}, [ana(1, { reactions: thumbs(ME) })]);
    expect(within(menu).getByRole("menuitemcheckbox", { name: "React with 👍" })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    expect(within(menu).getByRole("menuitemcheckbox", { name: "React with ❤️" })).toHaveAttribute(
      "aria-checked",
      "false",
    );
  });

  it("reacts with the emoji chosen, and closes", async () => {
    const { calls, menu } = await openMenu();
    await userEvent.click(within(menu).getByRole("menuitemcheckbox", { name: "React with 👍" }));
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    expect(posts(calls)).toHaveLength(1);
    expect(posts(calls)[0]?.body).toEqual({ emoji: "👍" });
    expect(
      await within(row(1)).findByRole("button", { name: "👍 1 reaction, yours" }),
    ).toBeInTheDocument();
  });

  it("is walked with the arrow keys from the first emoji down to the actions", async () => {
    const { menu } = await openMenu();
    await userEvent.keyboard("{ArrowDown}");
    expect(within(menu).getByRole("menuitemcheckbox", { name: "React with 👍" })).toHaveFocus();
    await userEvent.keyboard("{ArrowDown}{ArrowDown}{ArrowDown}{ArrowDown}{ArrowDown}{ArrowDown}");
    expect(within(menu).getByRole("menuitem", { name: "More reactions" })).toHaveFocus();
    await userEvent.keyboard("{ArrowDown}");
    expect(within(menu).getByRole("menuitem", { name: "Reply" })).toHaveFocus();
  });

  it("opens the picker where the menu was, and reacts with an emoji of it", async () => {
    const { calls, menu } = await openMenu();
    await userEvent.click(within(menu).getByRole("menuitem", { name: "More reactions" }));
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    const picker = await screen.findByRole("dialog", { name: "Pick a reaction" });
    await userEvent.click(within(picker).getByRole("button", { name: "React with 🚀" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(posts(calls)[0]?.body).toEqual({ emoji: "🚀" });
  });

  it("hangs the picker from the pointer, where the menu was", async () => {
    const { menu } = await openMenu();
    await userEvent.click(within(menu).getByRole("menuitem", { name: "More reactions" }));
    await screen.findByRole("dialog", { name: "Pick a reaction" });
    const anchor = document.querySelector<HTMLElement>("span.fixed.size-0");
    expect(anchor?.style.left).toBe("300px");
    expect(anchor?.style.top).toBe("200px");
  });

  it("closes the picker with Escape, reacting to nothing, and gives focus back to the row", async () => {
    const { calls, menu } = await openMenu();
    await userEvent.click(within(menu).getByRole("menuitem", { name: "More reactions" }));
    await screen.findByRole("dialog", { name: "Pick a reaction" });
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(posts(calls)).toHaveLength(0);
    await waitFor(() => expect(row(1)).toHaveFocus());
  });

  it("has no axe violations", async () => {
    const { menu } = await openMenu({}, [ana(1, { reactions: thumbs(ME) })]);
    expect(await axe(menu, AXE)).toHaveNoViolations();
  });
});

describe("the toolbar of a row (RCT-02)", () => {
  beforeEach(mouseDevice);

  async function rendered(over: Parameters<typeof renderTimeline>[1] = {}, items = [ana(1)]) {
    const out = renderTimeline(items, over, {
      "POST /rooms/r1/messages/m-r1-1/reactions": json(200, { items: thumbs(ME) }),
    });
    await screen.findByText("ana says 1");
    return out;
  }

  it("holds the six quick emoji and a button for more, on the row", async () => {
    await rendered();
    const bar = within(row(1)).getByRole("toolbar", { name: "Quick reactions" });
    expect(
      within(bar)
        .getAllByRole("button")
        .map((b) => b.getAttribute("aria-label")),
    ).toEqual([...QUICK_REACTIONS.map((e) => `React with ${e}`), "More reactions"]);
  });

  it("shows on hover and on keyboard focus of the row, and stays out of the way at rest", async () => {
    await rendered();
    const bar = within(row(1)).getByRole("toolbar", { name: "Quick reactions" });
    expect(bar).toHaveClass("opacity-0");
    expect(bar).toHaveClass("pointer-events-none");
    expect(bar).toHaveClass("group-hover/message:opacity-100");
    expect(bar).toHaveClass("group-hover/message:pointer-events-auto");
    expect(bar).toHaveClass("group-focus-within/message:opacity-100");
    expect(bar).toHaveClass("group-focus-within/message:pointer-events-auto");
    // It stays while its picker is open, though the pointer has left the row for it.
    expect(bar.className).toContain("has-[[aria-expanded=true]]:opacity-100");
  });

  it("sits beside the ⋯ button, which stays", async () => {
    await rendered();
    expect(
      within(row(1)).getByRole("button", { name: "Actions for the message from Ana" }),
    ).toBeInTheDocument();
  });

  it("reacts with the emoji tapped, and the chip is under the message", async () => {
    const { calls } = await rendered();
    await userEvent.click(within(row(1)).getByRole("button", { name: "React with 👍" }));
    expect(posts(calls)).toHaveLength(1);
    expect(
      await within(row(1)).findByRole("button", { name: "👍 1 reaction, yours" }),
    ).toBeInTheDocument();
    // The toolbar marks it too.
    expect(within(row(1)).getByRole("button", { name: "React with 👍" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("opens the picker from its last button", async () => {
    const { calls } = await rendered();
    await userEvent.click(within(row(1)).getByRole("button", { name: "More reactions" }));
    const picker = await screen.findByRole("dialog", { name: "Pick a reaction" });
    await userEvent.click(within(picker).getByRole("button", { name: "React with 🎯" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(posts(calls)[0]?.body).toEqual({ emoji: "🎯" });
  });

  it("is not there where the viewer may not react, or in a conversation that is history", async () => {
    const first = await rendered({ react: false });
    expect(within(row(1)).queryByRole("toolbar")).toBeNull();
    first.unmount();
    await rendered({ frozen: true });
    expect(within(row(1)).queryByRole("toolbar")).toBeNull();
  });

  it("is not there on a system notice or a deleted message", async () => {
    renderTimeline([
      ana(1),
      makeMessage({ seq: 2, author_kind: "system", text: "Ana joined" }),
      ana(3, { text: "", deleted_at: "2026-10-19T14:00:00Z" }),
    ]);
    await screen.findByText("Ana joined");
    expect(within(row(2)).queryByRole("toolbar")).toBeNull();
    expect(within(row(3)).queryByRole("toolbar")).toBeNull();
  });

  it("has no axe violations", async () => {
    await rendered();
    expect(await axe(row(1), AXE)).toHaveNoViolations();
  });
});

describe("a tap on a chip of a touch device (RCT-01)", () => {
  beforeEach(touchDevice);

  it("reacts and does not open the sheet", async () => {
    const { calls } = renderTimeline(
      [ana(1, { reactions: thumbs(THEM) })],
      {},
      {
        "POST /rooms/r1/messages/m-r1-1/reactions": json(200, { items: thumbs(THEM, ME) }),
      },
    );
    await screen.findByText("ana says 1");
    const chip = within(row(1)).getByRole("button", { name: "👍 1 reaction" });
    fireEvent.pointerDown(chip, finger());
    fireEvent.pointerUp(chip, finger());
    fireEvent.click(chip);
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    // Past the half second a press would have opened the sheet.
    await act(() => wait(HOLD));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(
      await within(row(1)).findByRole("button", { name: "👍 2 reactions, including yours" }),
    ).toBeInTheDocument();
  });
});
