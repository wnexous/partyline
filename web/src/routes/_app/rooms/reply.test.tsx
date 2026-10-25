import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Message, Room } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeConversation, makeIntelligence, makeMe, makeMessage, makeRoom } from "@/test/fixtures";
import { finger, longPress, mouseDevice, touchDevice } from "@/test/gestures";
import { stubLayout } from "@/test/layout";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * Answering a message from the conversation page (ACT-05, ACT-07): Reply in the actions of a
 * message puts it in the composer, and the page lets it go when the conversation changes.
 */

const me = makeMe();
const atlas = makeIntelligence({
  id: "a1",
  kind: "ai",
  role: "ai",
  display_name: "Atlas",
  handle: "atlas",
});

const room = (over: Partial<Room> = {}) =>
  makeRoom({ id: "g1", name: "release-crew", my_level: "post", member_count: 2, ...over });

const said = (seq: number, text: string): Message =>
  makeMessage({
    room_id: "g1",
    seq,
    author_kind: "agent",
    author_id: "a1",
    author_name: "Atlas",
    author: atlas,
    text,
  });

function serve(g1: Room = room()) {
  return mockApi({
    "GET /me": json(200, me),
    "GET /conversations": json(200, {
      items: [
        makeConversation({ id: "g1", title: "release-crew" }),
        makeConversation({ id: "g2", title: "other" }),
      ],
    }),
    "GET /rooms/g1": json(200, g1),
    "GET /rooms/g1/members": json(200, { items: [] }),
    "GET /rooms/g1/messages": json(200, {
      items: [said(2, "the build is green"), said(1, "starting the build")],
      has_more: false,
    }),
    "GET /rooms/g2": json(200, room({ id: "g2", name: "other" })),
    "GET /rooms/g2/members": json(200, { items: [] }),
    "GET /rooms/g2/messages": json(200, { items: [], has_more: false }),
    "PUT /rooms/g1/read": json(200, { room_id: "g1", seq: 2, unread: 0 }),
    "PUT /rooms/g2/read": json(200, { room_id: "g2", seq: 0, unread: 0 }),
    "POST /rooms/g1/messages": json(201, said(3, "sent")),
  });
}

const textbox = () => screen.findByRole("textbox", { name: "Message release-crew" });

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en", openRoomId: null, currentOrgId: null });
  window.localStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

describe("Reply on a phone (ACT-05, ACT-07)", () => {
  beforeEach(touchDevice);

  it("puts the message in the composer, with the cursor, and sends the answer with reply_to", async () => {
    const api = serve();
    renderRoute("/rooms/g1");
    const row = await screen.findByTestId("message-2");
    await longPress(row);
    await userEvent.click(await screen.findByRole("button", { name: "Reply" }));

    const strip = await screen.findByTestId("reply-strip");
    expect(within(strip).getByText("Replying to Atlas")).toBeInTheDocument();
    expect(within(strip).getByText("the build is green")).toBeInTheDocument();
    await waitFor(async () => expect(await textbox()).toHaveFocus());

    await userEvent.type(await textbox(), "thanks");
    await userEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() =>
      expect(
        api.calls.filter((c) => c.method === "POST" && c.path === "/rooms/g1/messages"),
      ).toHaveLength(1),
    );
    expect(api.calls.find((c) => c.method === "POST")?.body).toEqual({
      text: "thanks",
      reply_to: row.getAttribute("data-testid") === "message-2" ? "m-g1-2" : "",
    });
    await waitFor(() => expect(screen.queryByTestId("reply-strip")).toBeNull());
  });

  it("takes it back with Cancel reply, and sends nothing as an answer after", async () => {
    const api = serve();
    renderRoute("/rooms/g1");
    await longPress(await screen.findByTestId("message-1"));
    await userEvent.click(await screen.findByRole("button", { name: "Reply" }));
    await userEvent.click(await screen.findByRole("button", { name: "Cancel reply" }));
    expect(screen.queryByTestId("reply-strip")).toBeNull();
    await userEvent.type(await textbox(), "unrelated");
    await userEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(api.calls.find((c) => c.method === "POST")?.body).toBeDefined());
    expect(api.calls.find((c) => c.method === "POST")?.body).toEqual({ text: "unrelated" });
  });

  it("lets it go when the conversation changes, and does not bring it back", async () => {
    serve();
    const { router } = renderRoute("/rooms/g1");
    await longPress(await screen.findByTestId("message-2"));
    await userEvent.click(await screen.findByRole("button", { name: "Reply" }));
    await screen.findByTestId("reply-strip");

    await router.navigate({ to: "/rooms/$roomId", params: { roomId: "g2" } });
    await screen.findByRole("textbox", { name: "Message other" });
    expect(screen.queryByTestId("reply-strip")).toBeNull();
    await router.navigate({ to: "/rooms/$roomId", params: { roomId: "g1" } });
    await textbox();
    expect(screen.queryByTestId("reply-strip")).toBeNull();
  });

  it("offers no Reply to someone who may only read, and Copy text still", async () => {
    serve(room({ my_level: "view" }));
    renderRoute("/rooms/g1");
    await longPress(await screen.findByTestId("message-2"));
    const sheet = await screen.findByRole("dialog", { name: "Atlas" });
    expect(within(sheet).queryByRole("button", { name: "Reply" })).toBeNull();
    expect(within(sheet).getByRole("button", { name: "Copy text" })).toBeInTheDocument();
  });

  it("does not answer on a plain tap", async () => {
    serve();
    renderRoute("/rooms/g1");
    const row = await screen.findByTestId("message-2");
    const { fireEvent } = await import("@testing-library/react");
    fireEvent.pointerDown(row, finger());
    fireEvent.pointerUp(row, finger());
    fireEvent.click(row);
    expect(screen.queryByTestId("reply-strip")).toBeNull();
  });
});

describe("Reply with a mouse (ACT-02, ACT-07)", () => {
  beforeEach(mouseDevice);

  it("is on the ⋯ of the message, and puts it in the composer", async () => {
    serve();
    renderRoute("/rooms/g1");
    const row = await screen.findByTestId("message-2");
    await userEvent.click(
      within(row).getByRole("button", { name: "Actions for the message from Atlas" }),
    );
    await userEvent.click(await screen.findByRole("menuitem", { name: "Reply" }));
    expect(await screen.findByTestId("reply-strip")).toHaveTextContent("Replying to Atlas");
    await waitFor(async () => expect(await textbox()).toHaveFocus());
  });
});
