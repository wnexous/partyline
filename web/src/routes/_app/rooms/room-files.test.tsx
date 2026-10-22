import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Message, RoomFile } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeIntelligence, makeMe, makeMessage, makeRoom } from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * The Files tab through the room page (FILES-04): the header opens it, and "open in timeline"
 * brings the file's message into the timeline, loading older pages until it is there.
 */

const newest: Message[] = Array.from({ length: 50 }, (_, i) =>
  makeMessage({ seq: 51 + i, text: `recent ${51 + i}` }),
);
const older: Message[] = Array.from({ length: 50 }, (_, i) =>
  makeMessage({ seq: 1 + i, text: i === 29 ? "here is the contract" : `early ${1 + i}` }),
);
const contract: RoomFile = {
  id: "f1",
  name: "contract.pdf",
  mime: "application/pdf",
  size: 4096,
  status: "stored",
  kind: "document",
  message_id: "m-r1-30",
  message_seq: 30,
  author_kind: "user",
  author_name: "Ana",
  author: makeIntelligence({ display_name: "Ana", handle: "ana" }),
  created_at: "2026-10-19T12:00:00Z",
};

function roomApi() {
  return mockApi({
    "GET /me": json(200, makeMe()),
    "GET /rooms/r1": json(200, makeRoom({ id: "r1", name: "deploys" })),
    "GET /rooms/r1/members": json(200, { items: [] }),
    "GET /rooms/r1/messages": (url) =>
      url.searchParams.get("before_seq") === "51"
        ? json(200, { items: older, has_more: false })
        : json(200, { items: newest, has_more: true }),
    "GET /rooms/r1/attachments": json(200, { items: [contract], has_more: false }),
  });
}

stubLayout();
beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => {
  vi.unstubAllGlobals();
  delete (HTMLElement.prototype as unknown as Record<string, unknown>).scrollHeight;
});

describe("room page files tab (FILES-04)", () => {
  it("opens the Files tab from the header and lists the room's files", async () => {
    roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await screen.findByRole("complementary", { name: "Members" });

    await user.click(screen.getByRole("button", { name: "Files" }));
    const panel = await screen.findByRole("complementary", { name: "Files" });
    expect(within(panel).getByRole("tab", { name: "Files" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(await within(panel).findByTestId("file-f1")).toHaveTextContent("contract.pdf");

    await user.click(within(panel).getByRole("tab", { name: /Members/ }));
    expect(await screen.findByRole("complementary", { name: "Members" })).toBeInTheDocument();
  });

  it("jumps to the file's message, loading older messages until it is in view", async () => {
    // happy-dom has no scrolling: give the log a scroll height and record where the timeline
    // is asked to scroll.
    Object.defineProperty(HTMLElement.prototype, "scrollHeight", {
      configurable: true,
      get: () => 40 + 100 * 60 + 12,
    });
    const tops: number[] = [];
    const scrollTo = vi
      .spyOn(HTMLElement.prototype, "scrollTo")
      .mockImplementation((opts?: ScrollToOptions | number) => {
        if (typeof opts === "object" && opts.top !== undefined) tops.push(opts.top);
      });
    const { calls } = roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    const log = await screen.findByRole("log", { name: "Messages" });
    await within(log).findByText("recent 51");

    await user.click(screen.getByRole("button", { name: "Files" }));
    tops.length = 0;
    await user.click(
      await screen.findByRole("button", { name: "Open contract.pdf in the timeline" }),
    );
    await waitFor(() =>
      expect(
        calls.some(
          (c) => c.path === "/rooms/r1/messages" && c.url.searchParams.get("before_seq") === "51",
        ),
      ).toBe(true),
    );
    // The contract message is row 29 of 100 (60 px rows, 800 px viewport, 40 px top strip):
    // the timeline scrolls so that row is inside the viewport, not to the newest messages.
    const rowTop = 40 + 29 * 60;
    await waitFor(() =>
      expect(tops.some((top) => top <= rowTop && rowTop + 60 <= top + 800)).toBe(true),
    );
    scrollTo.mockRestore();
  });
});
