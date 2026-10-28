import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Message, Reaction, Room } from "@/shared/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeIntelligence, makeMe, makeMessage, makeRoom } from "@/test/fixtures";
import { mouseDevice } from "@/test/gestures";
import { stubLayout } from "@/test/layout";
import { renderRoute } from "@/test/render";

/*
 * Reacting from a conversation (RCT-01, RCT-02, RCT-06): who is offered the bar and the toolbar, what a tap
 * puts on the page, and a reaction of someone else arriving on the live socket.
 */

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));
const toast = vi.hoisted(() => Object.assign(vi.fn(), { error: vi.fn() }));
vi.mock("sonner", async (importOriginal) => ({ ...(await importOriginal<object>()), toast }));

const bobRef = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
const bobs = makeMessage({
  seq: 1,
  author_id: "u2",
  author_name: "Bob Lima",
  author: bobRef,
  text: "see you",
});
const thumbs = (...by: string[]): Reaction[] => [{ emoji: "👍", by }];

function page(items: Message[]) {
  return json(200, { items: [...items].reverse(), has_more: false });
}

function roomApi(
  room: Partial<Room> = {},
  messages: Message[] = [bobs],
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

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  mouseDevice();
  toast.mockClear();
  toast.error.mockClear();
});
afterEach(() => vi.unstubAllGlobals());

describe("who may react (RCT-03)", () => {
  it("offers the toolbar to whoever may post", async () => {
    roomApi({ my_level: "post" });
    renderRoute("/rooms/r1");
    await screen.findByText("see you");
    expect(within(row(1)).getByRole("toolbar", { name: "Quick reactions" })).toBeInTheDocument();
  });

  it("offers it to the manager of a group too", async () => {
    roomApi({ my_level: "manage" });
    renderRoute("/rooms/r1");
    await screen.findByText("see you");
    expect(within(row(1)).getByRole("toolbar", { name: "Quick reactions" })).toBeInTheDocument();
  });

  it("offers it to no one who can only read, who still sees the chips", async () => {
    roomApi({ my_level: "view" }, [{ ...bobs, reactions: thumbs("u2") }]);
    renderRoute("/rooms/r1");
    await screen.findByText("see you");
    expect(within(row(1)).queryByRole("toolbar")).toBeNull();
    const list = within(row(1)).getByRole("list", { name: "Reactions" });
    expect(within(list).queryByRole("button")).toBeNull();
    expect(within(list).getByText("👍 1 reaction")).toBeInTheDocument();
  });
});

describe("reacting from the page (RCT-01, RCT-06)", () => {
  it("puts the reaction under the message and sends it", async () => {
    const { calls } = roomApi({ my_level: "post" }, [bobs], {
      "POST /rooms/r1/messages/m-r1-1/reactions": json(200, { items: thumbs("u1") }),
    });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByText("see you");
    await user.click(within(row(1)).getByRole("button", { name: "React with 👍" }));
    expect(
      await within(row(1)).findByRole("button", { name: "👍 1 reaction, yours" }),
    ).toBeInTheDocument();
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ emoji: "👍" });
  });

  it("shows a reaction of someone else the moment the live frame arrives, without moving the message", async () => {
    roomApi({ my_level: "post" }, [bobs, makeMessage({ seq: 2, text: "later" })]);
    const { queryClient } = renderRoute("/rooms/r1");
    await screen.findByText("later");
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.reactions",
        data: { room_id: "r1", message_id: "m-r1-1", reactions: thumbs("u2", "u3") },
      }),
    );
    expect(
      await within(row(1)).findByRole("button", { name: "👍 2 reactions" }),
    ).toBeInTheDocument();
    expect(screen.getAllByTestId(/^message-\d+$/).map((el) => el.dataset.testid)).toEqual([
      "message-1",
      "message-2",
    ]);
  });

  it("opens the sheet's bar from a right click as a menu, and reacts from it", async () => {
    const { calls } = roomApi({ my_level: "post" }, [bobs], {
      "POST /rooms/r1/messages/m-r1-1/reactions": json(200, { items: thumbs("u1") }),
    });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByText("see you");
    fireEvent.contextMenu(row(1), { clientX: 200, clientY: 120 });
    const menu = await screen.findByRole("menu", { name: "Actions for the message from Bob Lima" });
    await user.click(within(menu).getByRole("menuitemcheckbox", { name: "React with 🙏" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ emoji: "🙏" });
  });
});

describe("a conversation that is history (CID-21)", () => {
  it("offers no reaction, and shows the chips it has", async () => {
    roomApi(
      {
        kind: "dm",
        my_level: "post",
        participants: [
          makeIntelligence({ id: "u1" }),
          makeIntelligence({
            id: "p1",
            role: "person",
            platform: "whatsapp",
            display_name: "Dana",
            handle: "dana",
          }),
        ],
      },
      [{ ...bobs, reactions: thumbs("u2") }],
    );
    renderRoute("/rooms/r1");
    await screen.findByTestId("history-notice");
    await screen.findByText("see you");
    expect(within(row(1)).queryByRole("toolbar")).toBeNull();
    expect(within(row(1)).getByText("👍 1 reaction")).toBeInTheDocument();
  });
});
