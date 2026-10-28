import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import { useProfilePanelStore } from "@/hooks/use-profile-panel";
import type { Conversation, RoomMember } from "@/shared/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeConversation, makeIntelligence, makeMe, makeMember, makeRoom } from "@/test/fixtures";
import { finger, HOLD, longPress, mouseDevice, touchDevice, wait } from "@/test/gestures";
import { stubLayout } from "@/test/layout";
import { renderRoute } from "@/test/render";

expect.extend(matchers);

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));
const toast = vi.hoisted(() => Object.assign(vi.fn(), { error: vi.fn() }));
// Only `toast` is ours: the root route still draws the real <Toaster>.
vi.mock("sonner", async (importOriginal) => ({
  ...(await importOriginal<typeof import("sonner")>()),
  toast,
}));

/*
 * What a conversation of the sidebar offers on a long press, a right click or its "⋯" button
 * (ACT-01..ACT-04, ACT-06, ROOM-01): mark it read, look at who it is with, leave a group, delete
 * one. Each uses the route that was already there and the rule that route already has: a member
 * leaves by taking their own member row out, deleting a group needs `manage`, and neither is for
 * a direct message. The last two ask first.
 */

const AXE_OPTIONS = { rules: { "color-contrast": { enabled: false } } };

const atlas = makeIntelligence({
  id: "a1",
  kind: "ai",
  role: "ai",
  display_name: "Atlas",
  handle: "atlas",
});
const me = makeMe();

/** The viewer's own row in a group: the one leaving takes out. */
const ana = makeMember({
  id: "mb-ana",
  kind: "user",
  member_id: "u1",
  name: "Ana Souza",
  owner: "",
});

const dm = (over: Partial<Conversation> = {}) =>
  makeConversation({
    id: "r2",
    kind: "dm",
    title: "Atlas",
    counterpart: atlas,
    my_level: "post",
    unread: 3,
    ...over,
  });
const group = (over: Partial<Conversation> = {}) =>
  makeConversation({ id: "r1", title: "deploys", my_level: "manage", unread: 0, ...over });

interface World {
  list: Conversation[];
  /** What the viewer's profile says: the app speaks it once the profile is loaded. */
  locale?: "en" | "pt-BR";
  failDelete?: boolean;
  /** The member rows of r1; without one of the viewer, there is nothing for them to leave. */
  members?: RoomMember[];
  failLeave?: boolean;
}

function serve(world: World) {
  let list = world.list;
  const api = mockApi({
    "GET /me": json(200, makeMe({ locale: world.locale ?? "en" })),
    "GET /conversations": () => json(200, { items: list }),
    "GET /rooms/r1/members": () => json(200, { items: world.members ?? [] }),
    "PUT /rooms/r2/read": json(200, { room_id: "r2", seq: 9, unread: 0 }),
    "DELETE /rooms/r1/members/mb-ana": () => {
      if (world.failLeave) return json(500, { error: { code: "internal", message: "no" } });
      list = list.filter((c) => c.id !== "r1");
      return new Response(null, { status: 204 });
    },
    "DELETE /rooms/r1": () => {
      if (world.failDelete) return json(500, { error: { code: "internal", message: "no" } });
      list = list.filter((c) => c.id !== "r1");
      return new Response(null, { status: 204 });
    },
    "GET /rooms/r1": json(200, makeRoom({ id: "r1", name: "deploys", my_level: "manage" })),
    "GET /rooms/r1/messages": json(200, { items: [], has_more: false }),
  });
  return api;
}

const LIST = /^(Conversations|Conversas)$/;
const sidebar = () => screen.findByRole("list", { name: LIST });
const rowOf = async (name: RegExp | string) => within(await sidebar()).getByRole("link", { name });
/** The row while a modal dialog is open: the page behind it is hidden from assistive technology. */
const rowBehind = (name: RegExp | string) =>
  within(screen.getByRole("list", { name: LIST, hidden: true })).getByRole("link", {
    name,
    hidden: true,
  });

const calls = (api: ReturnType<typeof serve>, method: string, path: string) =>
  api.calls.filter((c) => c.method === method && c.path === path);

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en", openRoomId: null, currentOrgId: null });
  window.localStorage.clear();
  toast.mockClear();
  toast.error.mockClear();
  useProfilePanelStore.setState({ id: null, opener: null });
});
afterEach(() => vi.unstubAllGlobals());

describe("a long press on a conversation (ACT-01, ACT-03)", () => {
  beforeEach(touchDevice);

  it("opens a sheet from the bottom named after the conversation", async () => {
    serve({ list: [group()] });
    renderRoute("/");
    await longPress(await rowOf(/deploys/));
    const sheet = await screen.findByRole("dialog", { name: "deploys" });
    expect(sheet).toHaveAccessibleDescription("Group");
    // Nothing waits in it, so there is nothing to mark: Delete group is all it offers.
    expect(within(sheet).getByRole("button", { name: "Delete group" })).toBeInTheDocument();
    expect(within(sheet).queryByRole("button", { name: "Mark as read" })).toBeNull();
  });

  it("says how much waits when something does, and that it is a direct message for a DM", async () => {
    serve({ list: [group({ unread: 1 }), dm({ unread: 3 })] });
    renderRoute("/");
    await longPress(await rowOf(/deploys/));
    expect(await screen.findByRole("dialog", { name: "deploys" })).toHaveAccessibleDescription(
      "Group · 1 unread message",
    );
  });

  it("does not open for a tap, and the tap still opens the conversation", async () => {
    serve({ list: [group()] });
    const { router } = renderRoute("/");
    const row = await rowOf(/deploys/);
    fireEvent.pointerDown(row, finger());
    fireEvent.pointerUp(row, finger());
    await userEvent.click(row);
    expect(screen.queryByRole("dialog", { name: "deploys" })).toBeNull();
    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/r1"));
  });

  it("does not open for a drag or a scroll", async () => {
    serve({ list: [group()] });
    renderRoute("/");
    const row = await rowOf(/deploys/);
    fireEvent.pointerDown(row, finger());
    fireEvent.pointerMove(row, finger({ clientY: 160 }));
    await act(() => wait(HOLD));
    expect(screen.queryByRole("dialog", { name: "deploys" })).toBeNull();
  });

  it("does not open the conversation when the finger lifts: that click was the end of the press", async () => {
    serve({ list: [group()] });
    const { router } = renderRoute("/");
    await longPress(await rowOf(/deploys/));
    await screen.findByRole("dialog", { name: "deploys" });
    expect(router.state.location.pathname).toBe("/");
  });

  it("opens from a screen reader's long click as well, which arrives as a context menu", async () => {
    serve({ list: [group()] });
    renderRoute("/");
    fireEvent.contextMenu(await rowOf(/deploys/), { clientX: 30, clientY: 40 });
    expect(await screen.findByRole("dialog", { name: "deploys" })).toBeInTheDocument();
  });

  it("closes with Escape and gives focus back to the row", async () => {
    serve({ list: [group()] });
    renderRoute("/");
    const row = await rowOf(/deploys/);
    await longPress(row);
    await screen.findByRole("dialog", { name: "deploys" });
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "deploys" })).toBeNull());
    await waitFor(() => expect(row).toHaveFocus());
  });

  it("has no axe violations", async () => {
    serve({ list: [group({ unread: 2 })] });
    renderRoute("/");
    await longPress(await rowOf(/deploys/));
    const sheet = await screen.findByRole("dialog", { name: "deploys" });
    expect(await axe(sheet, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("is in Portuguese when the app is", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    serve({ list: [group({ unread: 2 })], locale: "pt-BR" });
    renderRoute("/");
    await longPress(await rowOf(/deploys/));
    const sheet = await screen.findByRole("dialog", { name: "deploys" });
    expect(within(sheet).getByRole("button", { name: "Marcar como lida" })).toBeInTheDocument();
    expect(within(sheet).getByRole("button", { name: "Excluir grupo" })).toBeInTheDocument();
    expect(sheet).toHaveAccessibleDescription("Grupo · 2 mensagens não lidas");
  });
});

describe("what a conversation offers, and to whom (ACT-03)", () => {
  beforeEach(touchDevice);

  it("offers Mark as read only where something waits, and View profile only for a DM", async () => {
    serve({ list: [group({ my_level: "post" }), dm()] });
    renderRoute("/");
    await longPress(await rowOf(/Atlas/));
    const sheet = await screen.findByRole("dialog", { name: "Atlas" });
    expect(sheet).toHaveAccessibleDescription("Direct message · 3 unread messages");
    expect(
      within(sheet)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual([
      "Mark as read",
      "View profile",
      // The last one is the sheet's own close button.
      "",
    ]);
  });

  it("offers a DM no Delete, whatever the level says", async () => {
    serve({ list: [dm({ my_level: "manage" })] });
    renderRoute("/");
    await longPress(await rowOf(/Atlas/));
    const sheet = await screen.findByRole("dialog", { name: "Atlas" });
    expect(within(sheet).queryByRole("button", { name: /Delete group/ })).toBeNull();
  });

  it("offers a group that is read, to someone who only posts in it, Leave group", async () => {
    const api = serve({ list: [group({ my_level: "post", unread: 0 })], members: [ana] });
    renderRoute("/");
    await longPress(await rowOf(/deploys/));
    const sheet = await screen.findByRole("dialog", { name: "deploys" });
    expect(await within(sheet).findByRole("button", { name: "Leave group" })).toBeInTheDocument();
    expect(within(sheet).queryByRole("button", { name: "Delete group" })).toBeNull();
    // The viewer's row is the one that leaves: it has to be looked up.
    expect(calls(api, "GET", "/rooms/r1/members")).toHaveLength(1);
  });

  it("offers Delete group to whoever manages a group, last, in the destructive colour", async () => {
    serve({ list: [group({ unread: 2 })] });
    renderRoute("/");
    await longPress(await rowOf(/deploys/));
    const sheet = await screen.findByRole("dialog", { name: "deploys" });
    expect(
      within(sheet)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual(["Mark as read", "Delete group", ""]);
    expect(within(sheet).getByRole("button", { name: "Delete group" })).toHaveClass(
      "text-destructive",
    );
  });

  it("offers no Leave group when the group lists no member row of the viewer", async () => {
    // A manager who created the group has no member row to take out; only a member has one.
    const api = serve({ list: [group({ unread: 2 })] });
    renderRoute("/");
    await longPress(await rowOf(/deploys/));
    const sheet = await screen.findByRole("dialog", { name: "deploys" });
    await waitFor(() => expect(calls(api, "GET", "/rooms/r1/members")).toHaveLength(1));
    expect(within(sheet).queryByRole("button", { name: /Leave/ })).toBeNull();
  });
});

describe("Mark as read (ACT-03)", () => {
  beforeEach(touchDevice);

  it("reads the conversation up to its newest message and takes the count away", async () => {
    const api = serve({ list: [dm({ unread: 3 })] });
    renderRoute("/");
    const row = await rowOf(/Atlas/);
    expect(within(row).getByText("3 unread messages")).toBeInTheDocument();
    await longPress(row);
    await userEvent.click(await screen.findByRole("button", { name: "Mark as read" }));
    await waitFor(() => expect(calls(api, "PUT", "/rooms/r2/read")).toHaveLength(1));
    expect(calls(api, "PUT", "/rooms/r2/read")[0]?.body).toEqual({});
    await waitFor(() => expect(within(row).queryByText(/unread/)).toBeNull());
    expect(screen.queryByRole("dialog", { name: "Atlas" })).toBeNull();
  });

  it("says so when it could not", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [dm()] }),
      "PUT /rooms/r2/read": json(500, { error: { code: "internal", message: "no" } }),
    });
    renderRoute("/");
    await longPress(await rowOf(/Atlas/));
    await userEvent.click(await screen.findByRole("button", { name: "Mark as read" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn’t mark Atlas as read."));
  });
});

describe("View profile (ACT-03)", () => {
  beforeEach(touchDevice);

  it("opens the profile panel of the one a DM is with, and does not leave the list", async () => {
    serve({ list: [dm({ unread: 0 })] });
    const { router } = renderRoute("/");
    const row = await rowOf(/Atlas/);
    await longPress(row);
    await userEvent.click(await screen.findByRole("button", { name: "View profile" }));
    expect(useProfilePanelStore.getState().id).toBe("a1");
    // The panel gives focus back to the row when it closes.
    expect(useProfilePanelStore.getState().opener).toBe(row);
    expect(router.state.location.pathname).toBe("/");
  });
});

describe("Leave group (ACT-03, ROOM-01)", () => {
  beforeEach(touchDevice);

  const chooseLeave = async () => {
    await longPress(await rowOf(/deploys/));
    await userEvent.click(await screen.findByRole("button", { name: "Leave group" }));
    return screen.findByRole("alertdialog", { name: "Leave deploys?" });
  };

  it("is offered to a member of a group and never to a DM", async () => {
    serve({ list: [group({ my_level: "post" }), dm()], members: [ana] });
    renderRoute("/");
    await longPress(await rowOf(/deploys/));
    const sheet = await screen.findByRole("dialog", { name: "deploys" });
    expect(await within(sheet).findByRole("button", { name: "Leave group" })).toBeInTheDocument();

    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await longPress(await rowOf(/Atlas/));
    const dmSheet = await screen.findByRole("dialog", { name: "Atlas" });
    expect(within(dmSheet).queryByRole("button", { name: /Leave/ })).toBeNull();
  });

  it("asks first, then takes the viewer's own member row out and leaves the list", async () => {
    const api = serve({ list: [group({ my_level: "post" }), dm({ unread: 0 })], members: [ana] });
    renderRoute("/");
    const dialog = await chooseLeave();
    expect(dialog).toHaveAccessibleDescription(/leaves your conversations/);
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toHaveFocus();
    expect(calls(api, "DELETE", "/rooms/r1/members/mb-ana")).toHaveLength(0);

    await userEvent.click(within(dialog).getByRole("button", { name: "Leave group" }));
    await waitFor(() => expect(calls(api, "DELETE", "/rooms/r1/members/mb-ana")).toHaveLength(1));
    await waitFor(() =>
      expect(
        within(screen.getByRole("list", { name: LIST })).queryByRole("link", {
          name: /deploys/,
        }),
      ).toBeNull(),
    );
    expect(toast).toHaveBeenCalledWith("You left deploys");
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("goes back to the list when the open conversation is the one left", async () => {
    serve({ list: [group({ my_level: "post" })], members: [ana] });
    const { router } = renderRoute("/rooms/r1");
    const dialog = await chooseLeave();
    await userEvent.click(within(dialog).getByRole("button", { name: "Leave group" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
  });

  it("keeps the conversation and says so when the server refuses", async () => {
    serve({
      list: [group({ my_level: "post" }), dm({ unread: 0 })],
      members: [ana],
      failLeave: true,
    });
    renderRoute("/");
    const dialog = await chooseLeave();
    await userEvent.click(within(dialog).getByRole("button", { name: "Leave group" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn’t leave deploys."));
    // The dialog stays to try again from, and the row is still behind it.
    expect(screen.getByRole("alertdialog", { name: "Leave deploys?" })).toBeInTheDocument();
    expect(rowBehind(/deploys/)).toBeInTheDocument();
  });

  it("has no axe violations in the dialog", async () => {
    serve({ list: [group({ my_level: "post" })], members: [ana] });
    renderRoute("/");
    const dialog = await chooseLeave();
    expect(await axe(dialog, AXE_OPTIONS)).toHaveNoViolations();
  });
});

describe("Delete group (ACT-03, ACT-04)", () => {
  beforeEach(touchDevice);

  const chooseDelete = async () => {
    await longPress(await rowOf(/deploys/));
    await userEvent.click(await screen.findByRole("button", { name: "Delete group" }));
    return screen.findByRole("alertdialog", { name: "Delete deploys?" });
  };

  it("asks first, naming the conversation, with Cancel under the finger's thumb and nothing sent", async () => {
    const api = serve({ list: [group()] });
    renderRoute("/");
    const dialog = await chooseDelete();
    expect(dialog).toHaveAccessibleDescription(/removed for everyone in it/);
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toHaveFocus();
    expect(calls(api, "DELETE", "/rooms/r1")).toHaveLength(0);
  });

  it("leaves everything as it was when it is cancelled, and gives focus back to the row", async () => {
    const api = serve({ list: [group()] });
    renderRoute("/");
    const row = await rowOf(/deploys/);
    const dialog = await chooseDelete();
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(calls(api, "DELETE", "/rooms/r1")).toHaveLength(0);
    expect(await rowOf(/deploys/)).toBe(row);
    await waitFor(() => expect(row).toHaveFocus());
  });

  it("deletes it once it is confirmed: the row goes, and the user is told", async () => {
    const api = serve({ list: [group(), dm({ unread: 0 })] });
    renderRoute("/");
    const dialog = await chooseDelete();
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete group" }));
    await waitFor(() => expect(calls(api, "DELETE", "/rooms/r1")).toHaveLength(1));
    await waitFor(() =>
      expect(
        within(screen.getByRole("list", { name: LIST })).queryByRole("link", {
          name: /deploys/,
        }),
      ).toBeNull(),
    );
    expect(toast).toHaveBeenCalledWith("deploys deleted");
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("keeps the conversation and says so when the server refuses", async () => {
    serve({ list: [group()], failDelete: true });
    renderRoute("/");
    const dialog = await chooseDelete();
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete group" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn’t delete deploys."));
    // The dialog stays to try again from, and the row is still behind it.
    expect(screen.getByRole("alertdialog", { name: "Delete deploys?" })).toBeInTheDocument();
    expect(rowBehind(/deploys/)).toBeInTheDocument();
  });

  it("goes back to the list when the open conversation is the one deleted", async () => {
    serve({ list: [group()] });
    const { router } = renderRoute("/rooms/r1");
    await longPress(await rowOf(/deploys/));
    await userEvent.click(await screen.findByRole("button", { name: "Delete group" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Delete deploys?" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete group" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
  });

  it("stays where it is when it was another conversation that was deleted", async () => {
    serve({ list: [group(), dm({ unread: 0 })] });
    const { router } = renderRoute("/rooms/r2");
    await longPress(await rowOf(/deploys/));
    await userEvent.click(await screen.findByRole("button", { name: "Delete group" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Delete deploys?" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Delete group" }));
    await waitFor(() => expect(toast).toHaveBeenCalledWith("deploys deleted"));
    expect(router.state.location.pathname).toBe("/rooms/r2");
  });

  it("has no axe violations in the dialog", async () => {
    serve({ list: [group()] });
    renderRoute("/");
    const dialog = await chooseDelete();
    expect(await axe(dialog, AXE_OPTIONS)).toHaveNoViolations();
  });
});

describe("a mouse (ACT-02)", () => {
  beforeEach(mouseDevice);

  it("opens a menu at the pointer on a right click, and the browser's own menu stays closed", async () => {
    serve({ list: [group({ unread: 2 })] });
    renderRoute("/");
    const row = await rowOf(/deploys/);
    expect(fireEvent.contextMenu(row, { clientX: 150, clientY: 90 })).toBe(false);
    const menu = await screen.findByRole("menu", { name: "deploys" });
    expect(within(menu).getByRole("menuitem", { name: "Mark as read" })).toBeInTheDocument();
    const anchor = document.querySelector<HTMLElement>(
      '[aria-haspopup="menu"][aria-label="deploys"]',
    );
    expect(anchor?.style.left).toBe("150px");
    expect(anchor?.style.top).toBe("90px");
  });

  it("offers the same actions in it", async () => {
    serve({ list: [group({ unread: 2 })] });
    renderRoute("/");
    fireEvent.contextMenu(await rowOf(/deploys/), { clientX: 150, clientY: 90 });
    const menu = await screen.findByRole("menu", { name: "deploys" });
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((i) => i.textContent),
    ).toEqual(["Mark as read", "Delete group"]);
  });

  it("has a ⋯ button on the row that opens the same menu, named after the conversation", async () => {
    serve({ list: [group({ unread: 2 })] });
    renderRoute("/");
    await sidebar();
    const more = screen.getByRole("button", { name: "Actions for deploys" });
    expect(more).toHaveAttribute("aria-haspopup", "menu");
    await userEvent.click(more);
    const menu = await screen.findByRole("menu", { name: "deploys" });
    expect(within(menu).getByRole("menuitem", { name: "Mark as read" })).toBeInTheDocument();
    expect(more).toHaveAttribute("aria-expanded", "true");
  });

  it("shows the ⋯ button on hover and on keyboard focus, never when it could do nothing", async () => {
    serve({
      list: [group({ my_level: "post", unread: 0 }), dm({ counterpart: undefined, unread: 0 })],
      members: [ana],
    });
    renderRoute("/");
    await sidebar();
    const more = screen.getByRole("button", { name: "Actions for deploys" });
    expect(more).toHaveClass("opacity-0");
    expect(more).toHaveClass("group-hover/row:opacity-100");
    expect(more).toHaveClass("group-focus-within/row:opacity-100");
    expect(screen.queryByRole("button", { name: "Actions for Atlas" })).toBeNull();
  });

  it("gives focus back to the ⋯ button when its menu closes", async () => {
    serve({ list: [group({ unread: 2 })] });
    renderRoute("/");
    await sidebar();
    const more = screen.getByRole("button", { name: "Actions for deploys" });
    await userEvent.click(more);
    await screen.findByRole("menu", { name: "deploys" });
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    await waitFor(() => expect(more).toHaveFocus());
  });

  it("gives focus back to the row after a right click", async () => {
    serve({ list: [group({ unread: 2 })] });
    renderRoute("/");
    const row = await rowOf(/deploys/);
    fireEvent.contextMenu(row, { clientX: 150, clientY: 90 });
    await screen.findByRole("menu", { name: "deploys" });
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(row).toHaveFocus());
  });

  it("opens under the row for the context-menu key or Shift+F10, which come with no position", async () => {
    serve({ list: [group({ unread: 2 })] });
    renderRoute("/");
    const row = await rowOf(/deploys/);
    row.getBoundingClientRect = () =>
      ({ left: 12, top: 100, right: 300, bottom: 160, width: 288, height: 60 }) as DOMRect;
    fireEvent.contextMenu(row, { clientX: 0, clientY: 0 });
    await screen.findByRole("menu", { name: "deploys" });
    const anchor = document.querySelector<HTMLElement>(
      '[aria-haspopup="menu"][aria-label="deploys"]',
    );
    expect(anchor?.style.left).toBe("12px");
    expect(anchor?.style.top).toBe("160px");
  });

  it("does nothing to a press of a finger on a laptop that has a touch screen: the mouse is the pointer", async () => {
    serve({ list: [group({ unread: 2 })] });
    renderRoute("/");
    const row = await rowOf(/deploys/);
    // A touch on such a device still arrives as a long press of ours and opens the menu, not a sheet.
    fireEvent.pointerDown(row, finger());
    await act(() => wait(HOLD));
    expect(await screen.findByRole("menu", { name: "deploys" })).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

describe("the row of a mouse and the row of a finger differ in two small things", () => {
  it("a finger's row does not select text: a long press must not start a selection", async () => {
    touchDevice();
    serve({
      list: [
        group({ unread: 2 }),
        dm({ id: "r5", title: "quiet", counterpart: undefined, unread: 0 }),
      ],
    });
    renderRoute("/");
    const row = await rowOf(/deploys/);
    expect(row).toHaveClass("select-none");
    expect(row.className).toContain("[-webkit-touch-callout:none]");
    // A row with no actions is a plain link and keeps its text selectable.
    expect(await rowOf(/quiet/)).not.toHaveClass("select-none");
  });

  it("a mouse's row keeps its text selectable, and its time gives way to the ⋯ while it is hovered or has focus", async () => {
    mouseDevice();
    serve({
      list: [
        group({
          unread: 2,
          last_message: {
            seq: 1,
            author_id: "u9",
            author_name: "Bob",
            text: "hi",
            created_at: "2026-10-19T12:00:00Z",
          },
        }),
      ],
    });
    renderRoute("/");
    const row = await rowOf(/deploys/);
    expect(row).not.toHaveClass("select-none");
    const time = row.querySelector("time") as HTMLElement;
    expect(time).toHaveClass("group-hover/row:opacity-0");
    expect(time).toHaveClass("group-focus-within/row:opacity-0");
  });
});

describe("a touch device has no ⋯ buttons (ACT-02)", () => {
  it("leaves the rows as they were: the long press is the way in", async () => {
    touchDevice();
    serve({ list: [group({ unread: 2 }), dm()] });
    renderRoute("/");
    await sidebar();
    expect(screen.queryByRole("button", { name: /^Actions for/ })).toBeNull();
  });
});
