import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MessageTimeline } from "@/components/message-timeline";
import type { Message, RoomMember } from "@/lib/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeMessage } from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { renderWithProviders } from "@/test/render";

const ME = "u1";

const chatMember: RoomMember = {
  id: "mb1",
  kind: "chat",
  member_id: "c1",
  name: "Ops on call",
  outbound: true,
  connector_kind: "telegram",
  chat_kind: "group",
  display_name: "",
  sign_outbound: true,
  delivery: "all",
  keywords: [],
  created_at: "2026-10-01T12:00:00Z",
};

function page(items: Message[], has_more = false) {
  // Newest first: the client must not rely on server order.
  return json(200, { items: [...items].reverse(), has_more });
}

function chat(seq: number, over: Partial<Message> = {}) {
  return makeMessage({
    seq,
    author_kind: "chat",
    author_id: "c1",
    author_user_id: undefined,
    author_name: "Ops on call",
    sender_id: "tg:1",
    sender_name: "Diego",
    ...over,
  });
}

function renderTimeline(members: RoomMember[] = [chatMember]) {
  return renderWithProviders(<MessageTimeline roomId="r1" meId={ME} members={members} />);
}

const row = (seq: number) => screen.getByTestId(`message-${seq}`);

stubLayout();
beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

describe("message item (UI-05)", () => {
  it("marks an unverified chat sender as untrusted and a sender verified as me as trusted", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        chat(1, { text: "restart the pool" }),
        chat(2, { author_user_id: ME, sender_name: "Ana (phone)", text: "yes" }),
        chat(3, { author_user_id: "u2", sender_name: "bruno", text: "fine" }),
        makeMessage({ seq: 4, author_kind: "agent", author_id: "a1", author_name: "bot" }),
        makeMessage({ seq: 5, author_kind: "user", author_name: "Ana Souza" }),
      ]),
    });
    renderTimeline();
    await screen.findByText("restart the pool");

    expect(within(row(1)).getByText("Untrusted")).toBeInTheDocument();
    expect(within(row(1)).queryByText("Trusted")).toBeNull();
    expect(within(row(2)).getByText("Trusted")).toBeInTheDocument();
    expect(within(row(2)).queryByText("Untrusted")).toBeNull();
    // Verified as another user: their agents trust it, the viewer's agents do not.
    expect(within(row(3)).getByText("Verified")).toBeInTheDocument();
    for (const seq of [4, 5]) {
      expect(within(row(seq)).queryByText(/^(Trusted|Untrusted|Verified)$/)).toBeNull();
    }
  });

  it("shows author kind, author name, chat and platform, and time", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        chat(1, { created_at: "2026-10-19T12:34:00Z" }),
        makeMessage({ seq: 2, author_kind: "agent", author_id: "a1", author_name: "release-bot" }),
        makeMessage({ seq: 3, author_kind: "user", author_name: "Ana Souza" }),
        makeMessage({ seq: 4, author_kind: "system", author_id: undefined, author_name: "" }),
      ]),
    });
    renderTimeline();
    await screen.findByTestId("message-1");

    expect(within(row(1)).getByText("Chat")).toBeInTheDocument();
    expect(within(row(1)).getByText("Diego")).toBeInTheDocument();
    expect(within(row(1)).getByText(/Ops on call/)).toBeInTheDocument();
    expect(within(row(1)).getByText(/telegram/i)).toBeInTheDocument();
    const time = within(row(1)).getByRole("time");
    expect(time).toHaveAttribute("datetime", "2026-10-19T12:34:00Z");
    expect(within(row(2)).getByText("Agent")).toBeInTheDocument();
    expect(within(row(2)).getByText("release-bot")).toBeInTheDocument();
    expect(within(row(3)).getByText("Person")).toBeInTheDocument();
    expect(within(row(4)).getByText("System")).toBeInTheDocument();
  });

  it("shows the message it replies to", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        chat(1, { id: "m-a", text: "can you restart the worker pool?" }),
        makeMessage({ seq: 2, reply_to: "m-a", text: "on it" }),
        makeMessage({ seq: 3, reply_to: "m-gone", text: "late reply" }),
      ]),
    });
    renderTimeline();
    await screen.findByText("on it");
    const quote = within(row(2)).getByTestId("reply-context");
    expect(quote).toHaveTextContent("Diego");
    expect(quote).toHaveTextContent("can you restart the worker pool?");
    expect(within(row(3)).getByTestId("reply-context")).toHaveTextContent(
      "Reply to an earlier message",
    );
  });

  it("shows images inline and other files as download links", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        makeMessage({
          seq: 1,
          attachments: [
            { id: "f1", name: "build.log", mime: "text/plain", size: 2048, status: "stored" },
            { id: "f2", name: "shot.png", mime: "image/png", size: 5000, status: "stored" },
            { id: "f3", name: "huge.mov", mime: "video/quicktime", size: 9e9, status: "too_large" },
            { id: "f4", name: "page.svg", mime: "image/svg+xml", size: 900, status: "stored" },
          ],
        }),
      ]),
    });
    renderTimeline();
    const img = await screen.findByRole("img", { name: "shot.png" });
    expect(img).toHaveAttribute("src", "/api/v1/attachments/f2");
    expect(img).toHaveAttribute("loading", "lazy");
    const file = screen.getByRole("link", { name: /build\.log/ });
    expect(file).toHaveAttribute("href", "/api/v1/attachments/f1");
    expect(file).toHaveAttribute("download");
    // SVG can carry script: never inlined as an image.
    expect(screen.queryByRole("img", { name: "page.svg" })).toBeNull();
    expect(screen.getByRole("link", { name: /page\.svg/ })).toHaveAttribute("download");
    // Not stored: no link to a file that does not exist.
    expect(screen.queryByRole("link", { name: /huge\.mov/ })).toBeNull();
    expect(screen.getByText(/huge\.mov/)).toBeInTheDocument();
  });

  it("reserves the inline image box before the image loads", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        makeMessage({
          seq: 1,
          attachments: [
            { id: "f2", name: "shot.png", mime: "image/png", size: 5000, status: "stored" },
          ],
        }),
      ]),
    });
    renderTimeline();
    const img = await screen.findByRole("img", { name: "shot.png" });
    // Attachment has no pixel size: a fixed 4:3 box (320 × 240 at most) keeps the row height
    // stable while the image loads; the image is contained inside it.
    expect(img).toHaveAttribute("width", "320");
    expect(img).toHaveAttribute("height", "240");
    expect(img.closest("a")).toHaveStyle({ aspectRatio: "4 / 3" });
  });
});

describe("safe rendering (UI-09)", () => {
  it("renders HTML in chat text as text", async () => {
    const evil = "<script>alert('x')</script><img src=x onerror=alert(1)>";
    mockApi({ "GET /rooms/r1/messages": page([chat(1, { text: evil })]) });
    const { container } = renderTimeline();
    await waitFor(() => expect(row(1)).toHaveTextContent(evil));
    expect(container.querySelector("script")).toBeNull();
    expect(container.querySelector("img")).toBeNull();
  });

  it("renders agent text as Markdown without raw HTML", async () => {
    const text =
      "**ready** see [docs](https://example.com/d) and [bad](javascript:alert(1))\n\n<script>alert('x')</script>\n\n![pixel](https://example.com/p.png)";
    mockApi({
      "GET /rooms/r1/messages": page([
        makeMessage({ seq: 1, author_kind: "agent", author_id: "a1", author_name: "bot", text }),
        makeMessage({ seq: 2, author_kind: "user", text: "**not markdown**" }),
      ]),
    });
    const { container } = renderTimeline();
    await screen.findByTestId("message-1");
    const strong = await within(row(1)).findByText("ready");
    expect(strong.tagName).toBe("STRONG");
    expect(row(1)).toHaveTextContent("<script>alert('x')</script>");
    expect(container.querySelector("script")).toBeNull();

    const docs = within(row(1)).getByRole("link", { name: "docs" });
    expect(docs).toHaveAttribute("href", "https://example.com/d");
    expect(docs).toHaveAttribute("target", "_blank");
    expect(docs.getAttribute("rel")).toContain("noopener");
    expect(docs.getAttribute("rel")).toContain("noreferrer");
    const bad = within(row(1)).getByText("bad");
    expect(bad.getAttribute("href") ?? "").not.toMatch(/javascript:/i);
    // Remote images in agent text are links, never loaded automatically.
    expect(container.querySelector('img[src^="https://example.com"]')).toBeNull();
    expect(within(row(1)).getByRole("link", { name: /pixel/ })).toHaveAttribute(
      "href",
      "https://example.com/p.png",
    );

    expect(row(2)).toHaveTextContent("**not markdown**");
  });
});

describe("timeline (UI-04)", () => {
  it("appends a live room.message without refetching", async () => {
    const { calls } = mockApi({
      "GET /rooms/r1/messages": page([makeMessage({ seq: 1, text: "first" })]),
    });
    const { queryClient } = renderTimeline();
    await screen.findByText("first");
    const fetches = calls.length;

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message",
        data: makeMessage({ seq: 2, text: "deploy finished" }),
      }),
    );
    expect(await screen.findByText("deploy finished")).toBeVisible();
    expect(row(2)).toBeInTheDocument();
    expect(calls.length).toBe(fetches);
  });

  it("shows messages oldest first whatever the page order", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([1, 2, 3].map((seq) => makeMessage({ seq }))),
    });
    renderTimeline();
    await screen.findByText("message 3");
    const order = screen.getAllByTestId(/^message-\d+$/).map((el) => el.dataset.testid);
    expect(order).toEqual(["message-1", "message-2", "message-3"]);
  });

  it("loads the older page with before_seq when scrolled to the top", async () => {
    const { calls } = mockApi({
      "GET /rooms/r1/messages": (url) =>
        url.searchParams.get("before_seq") === "51"
          ? page([makeMessage({ seq: 50, text: "older" })])
          : page(
              Array.from({ length: 50 }, (_, i) => makeMessage({ seq: 51 + i })),
              true,
            ),
    });
    renderTimeline();
    const log = await screen.findByRole("log");
    await screen.findByTestId("message-51");
    log.scrollTop = 0;
    fireEvent.scroll(log);
    await waitFor(() =>
      expect(calls.some((c) => c.url.searchParams.get("before_seq") === "51")).toBe(true),
    );
  });

  it("explains how to add members when the room has no messages (UI-05)", async () => {
    mockApi({ "GET /rooms/r1/messages": page([]) });
    renderTimeline();
    expect(await screen.findByRole("heading", { name: "No messages yet" })).toBeInTheDocument();
    expect(screen.getByText(/Add agents and chats from Members/)).toBeInTheDocument();
  });
});
