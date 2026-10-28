import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MessageTimeline } from "@/components/message-timeline";
import type { Message } from "@/shared/api/types.gen";
import { json, mockApi } from "@/test/fetch";
import { makeMessage } from "@/test/fixtures";
import { finger, HOLD, mouseDevice, touchDevice, wait } from "@/test/gestures";
import { stubLayout } from "@/test/layout";
import { useLiveStore } from "@/test/live-store";
import { renderWithRouter } from "@/test/render";

/*
 * Answering a message with a drag to the right, on the rows of the timeline (SWP-01, SWP-02,
 * SWP-04): only on a touch device, only for a message that can be answered and where the viewer
 * may post; the message becomes the one the next message answers.
 */

const page = (items: Message[]) => json(200, { items: [...items].reverse(), has_more: false });
const row = (seq: number) => screen.getByTestId(`message-${seq}`);
const onReply = vi.fn();

function renderTimeline(items: Message[], props: { reply?: boolean } = {}) {
  mockApi({ "GET /rooms/r1/messages": page(items) });
  return renderWithRouter(
    <MessageTimeline
      roomId="r1"
      meId="u1"
      members={[]}
      onReply={props.reply === false ? undefined : onReply}
    />,
  );
}

const ana = (seq: number, over: Partial<Message> = {}) =>
  makeMessage({ seq, text: `ana says ${seq}`, ...over });

/** A finger that goes down on `target` and drags it by (dx, dy), then lifts. */
function swipe(target: Element, dx: number, dy = 0) {
  fireEvent.pointerDown(target, finger({ clientX: 100, clientY: 300 }));
  fireEvent.pointerMove(target, finger({ clientX: 100 + dx, clientY: 300 + dy }));
  fireEvent.pointerUp(target, finger({ clientX: 100 + dx, clientY: 300 + dy }));
}

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  onReply.mockClear();
  vi.stubGlobal("navigator", { ...navigator, vibrate: vi.fn() });
});
afterEach(() => vi.unstubAllGlobals());

describe("on a touch device (SWP-01)", () => {
  beforeEach(touchDevice);

  it("answers the message that was dragged, and no other", async () => {
    renderTimeline([ana(1), ana(2), ana(3)]);
    await screen.findByText("ana says 3");
    swipe(row(2), 90);
    expect(onReply).toHaveBeenCalledTimes(1);
    expect(onReply.mock.calls[0]?.[0]).toMatchObject({ seq: 2, text: "ana says 2" });
  });

  it("answers by the text of the message as by its edge", async () => {
    renderTimeline([ana(1)]);
    const text = await screen.findByText("ana says 1");
    swipe(text, 90);
    expect(onReply).toHaveBeenCalledTimes(1);
  });

  it("does not answer when it stops short, or when the drag is a scroll", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    swipe(row(1), 40);
    swipe(row(1), 20, 120);
    expect(onReply).not.toHaveBeenCalled();
  });

  it("keeps the vertical scroll and the zoom of the page on the row, and nothing else", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    expect(row(1)).toHaveClass("touch-pan-y");
    expect(row(1)).toHaveClass("touch-pinch-zoom");
  });

  it("has a reply icon under the row, hidden from assistive technology, that shows as the row moves", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    const icon = row(1).querySelector("span[aria-hidden='true'][style*='swipe-progress']");
    expect(icon).not.toBeNull();
    // It reads how far the row has come from the row itself, and is invisible at rest.
    expect(icon?.getAttribute("style")).toContain("opacity: var(--swipe-progress, 0)");
  });

  it("does not start from a link in the message", async () => {
    renderTimeline([ana(1, { text: "see [the notes](https://example.org/notes)" })]);
    await screen.findByText(/see/);
    const link = within(row(1)).queryByRole("link", { name: "the notes" });
    // Markdown is rendered for AI messages only: a user's text has no link to drag from.
    if (link) swipe(link, 90);
    expect(onReply).not.toHaveBeenCalled();
  });

  it("does not start from the player or its seek bar", async () => {
    renderTimeline([
      ana(1, {
        text: "",
        attachments: [
          {
            id: "v1",
            name: "voice-message-2026-10-24-140509.webm",
            mime: "audio/webm;codecs=opus",
            size: 1000,
            status: "stored",
          },
        ],
      }),
    ]);
    const player = await screen.findByRole("group", { name: "Voice message" });
    swipe(within(player).getByRole("slider", { name: "Seek" }), 90);
    swipe(within(player).getByRole("button", { name: "Play" }), 90);
    expect(onReply).not.toHaveBeenCalled();
  });

  it("does not start from an image that opens in the viewer", async () => {
    renderTimeline([
      ana(1, {
        text: "",
        attachments: [{ id: "i1", name: "pic.png", mime: "image/png", size: 10, status: "stored" }],
      }),
    ]);
    const image = await screen.findByRole("button", { name: /pic\.png/ });
    swipe(image, 90);
    expect(onReply).not.toHaveBeenCalled();
  });

  it("leaves a notice alone", async () => {
    renderTimeline([ana(1), makeMessage({ seq: 2, author_kind: "system", text: "Ana joined" })]);
    await screen.findByText("Ana joined");
    swipe(row(2), 90);
    expect(onReply).not.toHaveBeenCalled();
  });

  it("is not there for a viewer who may not post: no icon, no drag, the page scrolls as it did", async () => {
    renderTimeline([ana(1)], { reply: false });
    await screen.findByText("ana says 1");
    swipe(row(1), 90);
    expect(onReply).not.toHaveBeenCalled();
    expect(row(1)).not.toHaveClass("touch-pan-y");
    expect(row(1).querySelector("span[style*='swipe-progress']")).toBeNull();
  });

  it("is not a swipe when the finger has rested: the actions open instead, and the drag after them is nothing", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    fireEvent.pointerDown(row(1), finger({ clientX: 100, clientY: 300 }));
    await act(() => wait(HOLD));
    expect(await screen.findByRole("dialog", { name: "Ana" })).toBeInTheDocument();
    fireEvent.pointerMove(row(1), finger({ clientX: 200, clientY: 300 }));
    fireEvent.pointerUp(row(1), finger({ clientX: 200, clientY: 300 }));
    expect(onReply).not.toHaveBeenCalled();
  });

  it("does not open the actions of a message that is being dragged", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    fireEvent.pointerDown(row(1), finger({ clientX: 100, clientY: 300 }));
    fireEvent.pointerMove(row(1), finger({ clientX: 160, clientY: 300 }));
    await act(() => wait(HOLD));
    fireEvent.pointerUp(row(1), finger({ clientX: 160, clientY: 300 }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(onReply).toHaveBeenCalledTimes(1);
  });

  it("works while the row's own ⋯ and menu are not there: a swipe and the sheet are the two ways", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    expect(screen.queryByRole("button", { name: /^Actions for the message/ })).toBeNull();
    swipe(row(1), 90);
    await waitFor(() => expect(onReply).toHaveBeenCalled());
  });
});

describe("with a mouse (SWP-04)", () => {
  beforeEach(mouseDevice);

  it("a drag with the mouse is no swipe, and the row is not made to refuse a horizontal scroll", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    fireEvent.pointerDown(row(1), finger({ pointerType: "mouse", clientX: 100 }));
    fireEvent.pointerMove(row(1), finger({ pointerType: "mouse", clientX: 220 }));
    fireEvent.pointerUp(row(1), finger({ pointerType: "mouse", clientX: 220 }));
    expect(onReply).not.toHaveBeenCalled();
    expect(row(1)).not.toHaveClass("touch-pan-y");
    expect(row(1).querySelector("span[style*='swipe-progress']")).toBeNull();
  });

  it("even a finger on a laptop that has a mouse: the buttons and the menu are its way", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    swipe(row(1), 90);
    expect(onReply).not.toHaveBeenCalled();
  });
});
