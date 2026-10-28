import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Message, Room } from "@/shared/api/types.gen";
import { json, mockApi } from "@/test/fetch";
import { makeConversation, makeIntelligence, makeMe, makeMessage, makeRoom } from "@/test/fixtures";
import { longPress, touchDevice } from "@/test/gestures";
import { stubLayout } from "@/test/layout";
import { useLiveStore } from "@/test/live-store";
import { renderRoute } from "@/test/render";

vi.mock("@/app/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * Drafts (WA-07): what is typed, attached and chosen to answer in a conversation is still there
 * after going to another conversation and back; a send clears only its own conversation.
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

const said = (roomId: string, seq: number, text: string): Message =>
  makeMessage({
    room_id: roomId,
    seq,
    author_kind: "agent",
    author_id: "a1",
    author_name: "Atlas",
    author: atlas,
    text,
  });

function serve() {
  return mockApi({
    "GET /me": json(200, me),
    "GET /conversations": json(200, {
      items: [
        makeConversation({ id: "g1", title: "release-crew" }),
        makeConversation({ id: "g2", title: "other" }),
      ],
    }),
    "GET /contacts": json(200, []),
    "GET /rooms/g1": json(200, room()),
    "GET /rooms/g1/members": json(200, { items: [] }),
    "GET /rooms/g1/messages": json(200, {
      items: [said("g1", 2, "the build is green"), said("g1", 1, "starting the build")],
      has_more: false,
    }),
    "GET /rooms/g2": json(200, room({ id: "g2", name: "other" })),
    "GET /rooms/g2/members": json(200, { items: [] }),
    "GET /rooms/g2/messages": json(200, { items: [], has_more: false }),
    "PUT /rooms/g1/read": json(200, { room_id: "g1", seq: 2, unread: 0 }),
    "PUT /rooms/g2/read": json(200, { room_id: "g2", seq: 0, unread: 0 }),
    "POST /rooms/g1/messages": json(201, said("g1", 3, "sent")),
    "POST /rooms/g2/messages": json(201, said("g2", 1, "sent")),
  });
}

const boxOf = (name: string) => screen.findByRole("textbox", { name: `Message ${name}` });

async function open(router: ReturnType<typeof renderRoute>["router"], roomId: string) {
  await router.navigate({ to: "/rooms/$roomId", params: { roomId } });
}

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en", openRoomId: null, currentOrgId: null });
  window.localStorage.clear();
  touchDevice();
});
afterEach(() => vi.unstubAllGlobals());

describe("the composer draft survives switching conversations (WA-07)", () => {
  it("keeps the text, an attachment and the reply of a conversation when going to another and back", async () => {
    serve();
    const { router } = renderRoute("/rooms/g1");
    await longPress(await screen.findByTestId("message-2"));
    await userEvent.click(await screen.findByRole("button", { name: "Reply" }));
    await userEvent.type(await boxOf("release-crew"), "almost ready");
    await userEvent.upload(
      screen.getByLabelText("Attach files"),
      new File(["log line"], "build.log", { type: "text/plain" }),
    );
    expect(screen.getByText("build.log")).toBeInTheDocument();

    await open(router, "g2");
    expect(await boxOf("other")).toHaveValue("");
    expect(screen.queryByText("build.log")).toBeNull();
    expect(screen.queryByTestId("reply-strip")).toBeNull();

    await open(router, "g1");
    expect(await boxOf("release-crew")).toHaveValue("almost ready");
    expect(screen.getByText("build.log")).toBeInTheDocument();
    const strip = await screen.findByTestId("reply-strip");
    expect(within(strip).getByText("the build is green")).toBeInTheDocument();
  });

  it("keeps the draft when leaving the conversation for another page", async () => {
    serve();
    const { router } = renderRoute("/rooms/g1");
    await userEvent.type(await boxOf("release-crew"), "half a thought");
    await router.navigate({ to: "/contacts" });
    await screen.findByRole("heading", { name: "Contacts", level: 1 });
    await open(router, "g1");
    expect(await boxOf("release-crew")).toHaveValue("half a thought");
  });

  it("a successful send clears only the draft of its own conversation", async () => {
    const api = serve();
    const { router } = renderRoute("/rooms/g1");
    await userEvent.type(await boxOf("release-crew"), "for the crew");
    await open(router, "g2");
    await userEvent.type(await boxOf("other"), "for the others");

    await open(router, "g1");
    await userEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() =>
      expect(
        api.calls.filter((c) => c.method === "POST" && c.path === "/rooms/g1/messages"),
      ).toHaveLength(1),
    );
    await waitFor(async () => expect(await boxOf("release-crew")).toHaveValue(""));

    await open(router, "g2");
    expect(await boxOf("other")).toHaveValue("for the others");
  });
});
