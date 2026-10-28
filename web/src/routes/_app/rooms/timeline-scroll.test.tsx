import { act, fireEvent, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { createAppRouter } from "@/router";
import type { Message } from "@/shared/api/types.gen";
import { json, mockApi } from "@/test/fetch";
import { makeConversation, makeMe, makeMessage, makeRoom } from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { makeQueryClient, renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * Where a conversation opens (UI-26). The timeline is virtualized and pinned to the end: opening any
 * conversation lands on its newest message, whatever the one before it was scrolled to. The router
 * restores scroll positions by the DOM path of what scrolled, which is the same path in every
 * conversation, so it used to write the previous conversation's position into the new timeline:
 * that cancelled the timeline's own scroll to the end and left it somewhere in the middle, or at
 * the top.
 */

const me = makeMe();

const LONG = Array.from({ length: 60 }, (_, i) => i + 1);
const SHORT = [1, 2, 3];
/** The row the happy-dom timeline draws first: nothing really scrolls here, so the list starts at its top. */
const FIRST_OF_LONG = "long message 11";

const messagesOf = (room: string, seqs: number[]): Message[] =>
  seqs.map((seq) => makeMessage({ room_id: room, seq, text: `${room} message ${seq}` }));

function api(extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, me),
    "GET /conversations": json(200, {
      items: [
        makeConversation({ id: "long", title: "long" }),
        makeConversation({ id: "short", title: "short" }),
        makeConversation({ id: "empty", title: "empty" }),
      ],
    }),
    "GET /rooms/long": json(200, makeRoom({ id: "long", name: "long", my_level: "post" })),
    "GET /rooms/short": json(200, makeRoom({ id: "short", name: "short", my_level: "post" })),
    "GET /rooms/empty": json(200, makeRoom({ id: "empty", name: "empty", my_level: "post" })),
    "GET /rooms/long/members": json(200, { items: [] }),
    "GET /rooms/short/members": json(200, { items: [] }),
    "GET /rooms/empty/members": json(200, { items: [] }),
    "GET /rooms/long/messages": json(200, {
      // The newest page: the server answers newest first, and 50 at most.
      items: messagesOf("long", LONG.slice(-50)).reverse(),
      has_more: true,
    }),
    "GET /rooms/short/messages": json(200, {
      items: messagesOf("short", SHORT).reverse(),
      has_more: false,
    }),
    "GET /rooms/empty/messages": json(200, { items: [], has_more: false }),
    "PUT /rooms/long/read": json(200, { room_id: "long", seq: 60, unread: 0 }),
    "PUT /rooms/short/read": json(200, { room_id: "short", seq: 3, unread: 0 }),
    ...extra,
  });
}

type Router = ReturnType<typeof renderRoute>["router"];

const open = (router: Router, roomId: string) =>
  act(() => router.navigate({ to: "/rooms/$roomId", params: { roomId } }));

/**
 * Everything written to a timeline's `scrollTop`: the router restores a position that way, and the
 * timeline's own scrolls land here too, in happy-dom (0 and its end), so a test looks for the
 * position it left behind, not for silence.
 */
let writes: { target: Element; value: number }[];
let restoreSetter: () => void;

function spyOnScrollTopWrites() {
  writes = [];
  const owner = Object.getOwnPropertyDescriptor(Element.prototype, "scrollTop");
  if (!owner?.set || !owner.get) throw new Error("this DOM has no scrollTop accessor");
  const { get, set } = owner;
  Object.defineProperty(Element.prototype, "scrollTop", {
    configurable: true,
    get() {
      return get.call(this);
    },
    set(value: number) {
      writes.push({ target: this as Element, value });
      set.call(this, value);
    },
  });
  restoreSetter = () => Object.defineProperty(Element.prototype, "scrollTop", owner);
}

const timelineWrites = () =>
  writes.filter((w) => w.target.getAttribute("role") === "log").map((w) => w.value);

/** A scroll the way a browser reports one: the position, then the event the router listens to. */
function scrollTimelineTo(top: number) {
  const log = screen.getByRole("log");
  log.scrollTop = top;
  fireEvent.scroll(log);
}

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en", openRoomId: null, presence: {}, currentOrgId: null });
  spyOnScrollTopWrites();
});
afterEach(() => {
  restoreSetter();
  vi.unstubAllGlobals();
});

describe("opening a conversation lands on its newest message (UI-26)", () => {
  it("does not carry the position the previous conversation was left at into a cached one", async () => {
    api();
    const { router } = renderRoute("/rooms/long");
    await screen.findByText(FIRST_OF_LONG);

    // Away to a short conversation, left scrolled to 126 px.
    await open(router, "short");
    await screen.findByText("short message 3");
    scrollTimelineTo(126);

    // Back to the long one, whose pages are cached: its timeline exists the moment the route renders.
    writes.length = 0;
    await open(router, "long");
    await screen.findByText(FIRST_OF_LONG);
    await new Promise((done) => setTimeout(done, 50));

    // The router restores nothing into it: the 126 px of the other conversation never arrive.
    expect(timelineWrites()).not.toContain(126);
  });

  it("does not carry a position left near the top either", async () => {
    api();
    const { router } = renderRoute("/rooms/long");
    await screen.findByText(FIRST_OF_LONG);
    scrollTimelineTo(37);

    writes.length = 0;
    await open(router, "short");
    await screen.findByText("short message 3");
    await new Promise((done) => setTimeout(done, 50));
    expect(timelineWrites()).not.toContain(37);

    // And the other way round: from the short one, left at 41 px, to the long one cached.
    scrollTimelineTo(41);
    writes.length = 0;
    await open(router, "long");
    await screen.findByText(FIRST_OF_LONG);
    await new Promise((done) => setTimeout(done, 50));
    expect(timelineWrites()).not.toContain(41);
  });

  it("leaves the timeline alone going back through the browser history, as for a new visit", async () => {
    api();
    const { router, history } = renderRoute("/rooms/long");
    await screen.findByText(FIRST_OF_LONG);
    scrollTimelineTo(1234);

    await open(router, "short");
    await screen.findByText("short message 3");
    writes.length = 0;
    act(() => history.back());
    await screen.findByText(FIRST_OF_LONG);
    await new Promise((done) => setTimeout(done, 50));

    expect(timelineWrites()).not.toContain(1234);
  });

  it("opens an empty conversation on its empty state, and lands on its first message when it arrives", async () => {
    const { calls } = api();
    const { router, queryClient } = renderRoute("/rooms/long");
    await screen.findByText(FIRST_OF_LONG);

    await open(router, "empty");
    expect(await screen.findByRole("heading", { name: "No messages yet" })).toBeInTheDocument();
    expect(screen.queryByRole("log")).toBeNull();
    expect(calls.some((c) => c.path === "/rooms/empty/messages")).toBe(true);

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message",
        data: makeMessage({ room_id: "empty", seq: 1, text: "first words" }),
      }),
    );
    expect(await screen.findByText("first words")).toBeInTheDocument();
    expect(screen.getByRole("log")).toBeInTheDocument();
  });
});

describe("the router and conversations", () => {
  it("restores scroll positions of every page but a conversation", () => {
    const router = createAppRouter(makeQueryClient());
    const restores = router.options.scrollRestoration;
    if (typeof restores !== "function") throw new Error("expected a per-location policy");
    const at = (pathname: string) => restores({ location: { pathname } as never });

    expect(at("/rooms/01a16f2f-1092-705f-8d94-7643955fd6bb")).toBe(false);
    expect(at("/rooms/long")).toBe(false);
    for (const page of ["/", "/contacts", "/contacts/a1", "/orgs/o1/chart", "/account"]) {
      expect(at(page), page).toBe(true);
    }
  });
});
