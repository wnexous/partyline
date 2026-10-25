import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import {
  hasMessageActions,
  type MessageCapabilities,
  messageActionSet,
} from "@/components/message-menu";
import { MessageTimeline } from "@/components/message-timeline";
import type { Message } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
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
 * What a message offers on a long press, a right click or its "⋯" button (ACT-01, ACT-02, ACT-05,
 * ACT-06): Reply, and Copy text when it has some. A system notice offers nothing, and a viewer who
 * may not post is offered no Reply.
 */

const AXE_OPTIONS = { rules: { "color-contrast": { enabled: false } } };

const recording = {
  id: "v1",
  name: "voice-message-2026-10-24-140509.webm",
  mime: "audio/webm;codecs=opus",
  size: 41_230,
  status: "stored" as const,
};

const page = (items: Message[]) => json(200, { items: [...items].reverse(), has_more: false });
const row = (seq: number) => screen.getByTestId(`message-${seq}`);

const onReply = vi.fn();
let writeText: ReturnType<typeof vi.fn>;

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

// Ana is not the viewer (u1): of her messages the viewer may Reply and Copy text, not Edit or Delete.
const ana = (seq: number, over: Partial<Message> = {}) =>
  makeMessage({
    seq,
    text: `ana says ${seq}`,
    author_id: "u2",
    author: { id: "u2", kind: "human", role: "user", display_name: "Ana", handle: "ana" },
    ...over,
  });

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  onReply.mockClear();
  toast.mockClear();
  toast.error.mockClear();
  writeText = vi.fn(async () => {});
  vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
});
afterEach(() => vi.unstubAllGlobals());

describe("a long press on a message (ACT-01, ACT-05)", () => {
  beforeEach(touchDevice);

  it("opens a sheet named after its author, with a line of what it says", async () => {
    renderTimeline([ana(1, { text: "the deploy\n finished   at nine" })]);
    await screen.findByText(/the deploy/);
    await longPress(row(1));
    const sheet = await screen.findByRole("dialog", { name: "Ana" });
    expect(sheet).toHaveAccessibleDescription("the deploy finished at nine");
    expect(within(sheet).getByRole("button", { name: "Reply" })).toBeInTheDocument();
    expect(within(sheet).getByRole("button", { name: "Copy text" })).toBeInTheDocument();
  });

  it("opens for the message pressed, not for its neighbour", async () => {
    renderTimeline([ana(1), ana(2, { text: "second", author_name: "Bruno" })]);
    await screen.findByText("second");
    await longPress(row(2));
    expect(await screen.findByRole("dialog", { name: "Bruno" })).toHaveAccessibleDescription(
      "second",
    );
  });

  it("does not open for a tap or a drag", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    fireEvent.pointerDown(row(1), finger());
    fireEvent.pointerUp(row(1), finger());
    fireEvent.click(row(1));
    fireEvent.pointerDown(row(1), finger());
    fireEvent.pointerMove(row(1), finger({ clientY: 200 }));
    await act(() => wait(HOLD));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("does not select the text under the finger: the sheet copies the whole message", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    expect(row(1)).toHaveClass("select-none");
    expect(row(1).className).toContain("[-webkit-touch-callout:none]");
  });

  it("answers a screen reader's long click, which arrives as a context menu", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    expect(fireEvent.contextMenu(row(1), { clientX: 20, clientY: 30 })).toBe(false);
    expect(await screen.findByRole("dialog", { name: "Ana" })).toBeInTheDocument();
  });

  it("is named by the name the room shows for an AI, not by its machine", async () => {
    renderTimeline([
      makeMessage({
        seq: 1,
        author_kind: "agent",
        author_id: "a1",
        author_name: "Atlas",
        author: { id: "a1", kind: "ai", role: "ai", display_name: "Atlas", handle: "atlas" },
        text: "build is green",
      }),
    ]);
    await screen.findByText("build is green");
    await longPress(row(1));
    expect(await screen.findByRole("dialog", { name: "Atlas" })).toBeInTheDocument();
  });

  it("closes with Escape", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    await longPress(row(1));
    await screen.findByRole("dialog", { name: "Ana" });
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Ana" })).toBeNull());
  });

  it("has no axe violations", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    await longPress(row(1));
    const sheet = await screen.findByRole("dialog", { name: "Ana" });
    expect(await axe(sheet, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("is in Portuguese when the app is", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    await longPress(row(1));
    const sheet = await screen.findByRole("dialog", { name: "Ana" });
    expect(within(sheet).getByRole("button", { name: "Responder" })).toBeInTheDocument();
    expect(within(sheet).getByRole("button", { name: "Copiar texto" })).toBeInTheDocument();
  });
});

describe("Reply (ACT-05, ACT-07)", () => {
  beforeEach(touchDevice);

  it("hands the message to whoever answers, and closes", async () => {
    renderTimeline([ana(1), ana(2)]);
    await screen.findByText("ana says 2");
    await longPress(row(2));
    await userEvent.click(await screen.findByRole("button", { name: "Reply" }));
    expect(onReply).toHaveBeenCalledTimes(1);
    expect(onReply.mock.calls[0]?.[0]).toMatchObject({ seq: 2, text: "ana says 2" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("is not offered where the viewer cannot post, and Copy text still is", async () => {
    renderTimeline([ana(1)], { reply: false });
    await screen.findByText("ana says 1");
    await longPress(row(1));
    const sheet = await screen.findByRole("dialog", { name: "Ana" });
    expect(within(sheet).queryByRole("button", { name: "Reply" })).toBeNull();
    expect(within(sheet).getByRole("button", { name: "Copy text" })).toBeInTheDocument();
  });
});

describe("Copy text (ACT-05)", () => {
  beforeEach(touchDevice);

  it("puts the whole text on the clipboard as it was written, and says so", async () => {
    renderTimeline([ana(1, { text: "line one\n\n`code`  and **bold**" })]);
    await screen.findByText(/line one/);
    await longPress(row(1));
    await userEvent.click(await screen.findByRole("button", { name: "Copy text" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("line one\n\n`code`  and **bold**"));
    // At the top of the screen, for a moment: the composer is at the bottom.
    await waitFor(() =>
      expect(toast).toHaveBeenCalledWith(
        "Text copied",
        expect.objectContaining({ position: "top-center", duration: 2000 }),
      ),
    );
  });

  it("says so when it could not copy", async () => {
    writeText.mockRejectedValue(new DOMException("no", "NotAllowedError"));
    document.execCommand = vi.fn(() => false);
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    await longPress(row(1));
    await userEvent.click(await screen.findByRole("button", { name: "Copy text" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        "Couldn’t copy the text.",
        expect.objectContaining({ position: "top-center" }),
      ),
    );
    expect(toast).not.toHaveBeenCalled();
  });

  it("is not offered for a message that has no text, which offers Reply alone", async () => {
    renderTimeline([ana(1, { text: "", attachments: [recording] })]);
    await screen.findByRole("group", { name: "Voice message" });
    await longPress(row(1));
    const sheet = await screen.findByRole("dialog", { name: "Ana" });
    expect(sheet).toHaveAccessibleDescription("Voice message");
    expect(within(sheet).getByRole("button", { name: "Reply" })).toBeInTheDocument();
    expect(within(sheet).queryByRole("button", { name: "Copy text" })).toBeNull();
  });
});

describe("what offers nothing (ACT-05)", () => {
  beforeEach(touchDevice);

  it("a system notice: no sheet, and the browser's own menu is left alone", async () => {
    renderTimeline([ana(1), makeMessage({ seq: 2, author_kind: "system", text: "Ana joined" })]);
    await screen.findByText("Ana joined");
    fireEvent.pointerDown(row(2), finger());
    await act(() => wait(HOLD));
    fireEvent.pointerUp(row(2), finger());
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(fireEvent.contextMenu(row(2))).toBe(true);
  });

  it("a message with nothing to copy, to someone who cannot post: no sheet", async () => {
    renderTimeline([ana(1, { text: "", attachments: [recording] })], { reply: false });
    await screen.findByRole("group", { name: "Voice message" });
    fireEvent.pointerDown(row(1), finger());
    await act(() => wait(HOLD));
    fireEvent.pointerUp(row(1), finger());
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(fireEvent.contextMenu(row(1))).toBe(true);
  });
});

const viewer = (over: Partial<MessageCapabilities> = {}): MessageCapabilities => ({
  meId: "u1",
  canReply: true,
  canModerate: false,
  canEdit: true,
  frozen: false,
  ...over,
});

describe("which messages have actions (ACT-05, EDT-10, DLT-09)", () => {
  const own = makeMessage({ author_kind: "user", author_id: "u1", text: "hi" });
  const other = makeMessage({ author_kind: "user", author_id: "u2", text: "hi" });

  it("none for a system notice, whoever looks", () => {
    const notice = makeMessage({ author_kind: "system", text: "Ana joined" });
    expect(hasMessageActions(notice, viewer())).toBe(false);
    expect(hasMessageActions(notice, viewer({ canReply: false }))).toBe(false);
    expect(hasMessageActions(notice, viewer({ canModerate: true }))).toBe(false);
  });

  it("none for a deleted message, whoever looks: a placeholder is no one's to answer, copy or delete", () => {
    const gone = makeMessage({
      author_kind: "user",
      author_id: "u1",
      text: "",
      deleted_at: "2026-10-19T14:00:00Z",
    });
    expect(hasMessageActions(gone, viewer())).toBe(false);
    expect(hasMessageActions(gone, viewer({ canModerate: true }))).toBe(false);
  });

  it("Reply for whoever may post, Copy text for whatever has text", () => {
    expect(hasMessageActions(other, viewer())).toBe(true);
    expect(hasMessageActions(other, viewer({ canReply: false }))).toBe(true);
    expect(hasMessageActions({ ...other, text: "" }, viewer())).toBe(true);
    expect(hasMessageActions({ ...other, text: "  \n " }, viewer({ canReply: false }))).toBe(false);
  });

  it("the author's own text message offers all four", () => {
    expect(messageActionSet(own, viewer())).toEqual({
      reply: true,
      copy: true,
      edit: true,
      remove: true,
    });
  });

  it("someone else's message offers Reply and Copy text, and Delete to the manager of the group alone", () => {
    expect(messageActionSet(other, viewer())).toEqual({
      reply: true,
      copy: true,
      edit: false,
      remove: false,
    });
    expect(messageActionSet(other, viewer({ canModerate: true }))).toEqual({
      reply: true,
      copy: true,
      edit: false,
      remove: true,
    });
  });

  it("a file with no caption: its author replies, edits (to add one) and deletes it, and there is no text to copy", () => {
    const fileOnly = makeMessage({
      author_kind: "user",
      author_id: "u1",
      text: "",
      attachments: [recording],
    });
    expect(messageActionSet(fileOnly, viewer())).toEqual({
      reply: true,
      copy: false,
      edit: true,
      remove: true,
    });
  });

  it("no Edit where there is no composer to edit in", () => {
    expect(messageActionSet(own, viewer({ canEdit: false }))).toMatchObject({
      edit: false,
      remove: true,
    });
  });

  it("a conversation that is history keeps Copy text and nothing else (CID-21)", () => {
    expect(messageActionSet(own, viewer({ frozen: true, canModerate: true }))).toEqual({
      reply: false,
      copy: true,
      edit: false,
      remove: false,
    });
  });
});

describe("a mouse (ACT-02)", () => {
  beforeEach(mouseDevice);

  it("opens a menu at the pointer on a right click, and the browser's own stays closed", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    expect(fireEvent.contextMenu(row(1), { clientX: 400, clientY: 220 })).toBe(false);
    const menu = await screen.findByRole("menu", { name: "Actions for the message from Ana" });
    expect(
      within(menu)
        .getAllByRole("menuitem")
        .map((i) => i.textContent),
    ).toEqual(["Reply", "Copy text"]);
    const anchor = document.querySelector<HTMLElement>(
      '[aria-haspopup="menu"][aria-hidden="true"]',
    );
    expect(anchor?.style.left).toBe("400px");
    expect(anchor?.style.top).toBe("220px");
  });

  it("has a ⋯ button on the row, named after the message, shown on hover and on focus", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    const more = within(row(1)).getByRole("button", { name: "Actions for the message from Ana" });
    expect(more).toHaveAttribute("aria-haspopup", "menu");
    expect(more).toHaveClass("opacity-0");
    expect(more).toHaveClass("group-hover/message:opacity-100");
    expect(more).toHaveClass("group-focus-within/message:opacity-100");
  });

  it("opens the same menu from the ⋯, and gives focus back to it on Escape", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    const more = within(row(1)).getByRole("button", { name: "Actions for the message from Ana" });
    await userEvent.click(more);
    await screen.findByRole("menu", { name: "Actions for the message from Ana" });
    expect(more).toHaveAttribute("aria-expanded", "true");
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    await waitFor(() => expect(more).toHaveFocus());
  });

  it("moves with the arrow keys and chooses with Enter", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    await userEvent.click(
      within(row(1)).getByRole("button", { name: "Actions for the message from Ana" }),
    );
    const menu = await screen.findByRole("menu");
    await userEvent.keyboard("{ArrowDown}");
    expect(within(menu).getByRole("menuitem", { name: "Reply" })).toHaveFocus();
    await userEvent.keyboard("{ArrowDown}{Enter}");
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("ana says 1"));
  });

  it("gives Reply focus to the composer, not back to the button", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    const more = within(row(1)).getByRole("button", { name: "Actions for the message from Ana" });
    await userEvent.click(more);
    await userEvent.click(await screen.findByRole("menuitem", { name: "Reply" }));
    expect(onReply).toHaveBeenCalledTimes(1);
    await act(() => wait(50));
    expect(more).not.toHaveFocus();
  });

  it("has no ⋯ on a notice, or on a message that offers nothing", async () => {
    renderTimeline(
      [makeMessage({ seq: 1, author_kind: "system", text: "Ana joined" }), ana(2, { text: "" })],
      { reply: false },
    );
    await screen.findByText("Ana joined");
    expect(screen.queryByRole("button", { name: /^Actions for the message/ })).toBeNull();
  });

  it("keeps the ⋯ clear of the time a grouped row shows on hover", async () => {
    renderTimeline([ana(1), ana(2)]);
    await screen.findByText("ana says 2");
    const grouped = within(row(2));
    const more = grouped.getByRole("button", { name: "Actions for the message from Ana" });
    const time = row(2).querySelector("time") as HTMLElement;
    // One flex box holds the two: they sit side by side by construction.
    expect(more.parentElement).toBe(time.parentElement?.parentElement);
    expect(more.parentElement).toHaveClass("flex");
  });

  it("has no axe violations in the menu", async () => {
    renderTimeline([ana(1)]);
    await screen.findByText("ana says 1");
    fireEvent.contextMenu(row(1), { clientX: 400, clientY: 220 });
    const menu = await screen.findByRole("menu");
    expect(await axe(menu, AXE_OPTIONS)).toHaveNoViolations();
  });
});

describe("a touch device has no ⋯ buttons (ACT-02)", () => {
  it("leaves the rows as they were", async () => {
    touchDevice();
    renderTimeline([ana(1), ana(2)]);
    await screen.findByText("ana says 2");
    expect(screen.queryByRole("button", { name: /^Actions for the message/ })).toBeNull();
  });
});
