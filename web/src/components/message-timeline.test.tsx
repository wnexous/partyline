import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MessageTimeline } from "@/components/message-timeline";
import type { Message, RoomMember } from "@/shared/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeIntelligence, makeMessage } from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { renderWithRouter } from "@/test/render";

const ME = "u1";

const chatMember: RoomMember = {
  id: "mb1",
  kind: "chat",
  member_id: "c1",
  name: "Ops on call",
  connector_kind: "telegram",
  chat_kind: "group",
  display_name: "",
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
  // The author name and face link to the profile (PDM-03), so the timeline needs a router.
  return renderWithRouter(<MessageTimeline roomId="r1" meId={ME} members={members} />);
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

  it("shows author name, chat and platform, and time, and names an AI or a person by no kind (AD-024)", async () => {
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
    // Where a chat or the system speaks says where the text came from; an AI and a person are
    // just their names, for the eye and for a screen reader.
    expect(within(row(2)).getByText("release-bot")).toBeInTheDocument();
    expect(within(row(3)).getByText("Ana Souza")).toBeInTheDocument();
    for (const seq of [2, 3]) {
      expect(within(row(seq)).queryByText(/^(Agent|Person|AI|IA)$/)).toBeNull();
      expect(row(seq).textContent).not.toMatch(/Agent|Person/);
    }
    expect(within(row(4)).getByText("System")).toBeInTheDocument();
  });

  it("names the chat from message.chat with a platform badge (CTX-07)", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        // The room member list knows nothing about these chats: the message carries them.
        chat(1, {
          author_id: "c7",
          author_name: "stale name",
          chat: { id: "c7", title: "Front desk", kind: "group", platform: "whatsapp" },
        }),
        chat(2, {
          author_id: "c8",
          author_name: "",
          sender_name: "Lia",
          chat: { id: "c8", title: "  ", kind: "dm", platform: "telegram" },
        }),
        chat(3, {
          author_id: "c9",
          author_name: "",
          chat: { id: "c9", title: "", kind: "group", platform: "matrix" },
        }),
      ]),
    });
    renderTimeline([]);
    await screen.findByTestId("message-1");

    expect(within(row(1)).getByText("via Front desk")).toBeInTheDocument();
    expect(within(row(1)).getByTestId("chat-platform")).toHaveTextContent(/^WhatsApp$/);
    expect(within(row(1)).queryByText(/stale name/)).toBeNull();
    // Untitled chats fall back to "<Platform> chat", like the server's room names.
    expect(within(row(2)).getByText("via Telegram chat")).toBeInTheDocument();
    expect(within(row(2)).getByTestId("chat-platform")).toHaveTextContent(/^Telegram$/);
    expect(within(row(3)).getByText("via Matrix chat")).toBeInTheDocument();
  });

  it("falls back to the author name and member platform without message.chat", async () => {
    mockApi({ "GET /rooms/r1/messages": page([chat(1)]) });
    renderTimeline();
    await screen.findByTestId("message-1");
    expect(within(row(1)).getByText("via Ops on call · Telegram")).toBeInTheDocument();
    expect(within(row(1)).queryByTestId("chat-platform")).toBeNull();
  });

  it("names untitled chats in Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    mockApi({
      "GET /rooms/r1/messages": page([
        chat(1, {
          author_name: "",
          chat: { id: "c1", title: "", kind: "dm", platform: "whatsapp" },
        }),
      ]),
    });
    renderTimeline();
    await screen.findByTestId("message-1");
    expect(within(row(1)).getByText("via Chat do WhatsApp")).toBeInTheDocument();
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
    expect(img.closest("button")).toHaveStyle({ aspectRatio: "4 / 3" });
  });

  it("renders a message without delivery badges while chat history keeps its platform (CID-22)", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        chat(1, {
          text: "restart the pool",
          chat: { id: "c1", title: "Ops on call", kind: "group", platform: "telegram" },
        }),
        makeMessage({ seq: 2, text: "restarted" }),
      ]),
    });
    renderTimeline();
    await screen.findByText("restarted");
    // Delivery to chats is retired: no chip, no "to <chat>" and no error text.
    expect(within(row(2)).queryByText(/^(Queued|Sending|Delivered|Failed)$/)).toBeNull();
    expect(within(row(2)).queryByText(/^to /)).toBeNull();
    expect(within(row(2)).getByText("restarted")).toBeInTheDocument();
    // History written by a chat still says where it came from.
    expect(within(row(1)).getByTestId("chat-platform")).toHaveTextContent(/^Telegram$/);
  });
});

describe("message author (PROF-03, PROF-09, GRP-03)", () => {
  const atlas = makeIntelligence({
    id: "a1",
    kind: "ai",
    role: "ai",
    display_name: "Atlas",
    handle: "atlas",
  });
  const lia = makeIntelligence({
    id: "p1",
    role: "person",
    platform: "whatsapp",
    display_name: "Lia Costa",
    handle: "lia",
  });
  const ana = makeIntelligence();

  it("shows an AI's avatar and name with no AI badge, and its @handle on focus (PROF-09, AD-024)", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        makeMessage({
          seq: 1,
          author_kind: "agent",
          author_id: "a1",
          author_name: "Atlas",
          author: atlas,
        }),
      ]),
    });
    renderTimeline();
    await screen.findByTestId("message-1");
    expect(within(row(1)).getByTestId("intelligence-avatar")).toHaveTextContent("A");
    expect(within(row(1)).queryByText(/^(AI|IA)$/)).toBeNull();
    expect(within(row(1)).queryByTestId("kind-badge")).toBeNull();
    expect(within(row(1)).queryByText("@atlas")).toBeNull();
    act(() => within(row(1)).getByRole("link", { name: "Atlas" }).focus());
    expect(await screen.findByRole("tooltip")).toHaveTextContent("@atlas");
  });

  it("links an author's name and face to its profile (PDM-03)", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        makeMessage({
          seq: 1,
          author_kind: "agent",
          author_id: "a1",
          author_name: "Atlas",
          author: atlas,
        }),
      ]),
    });
    renderTimeline();
    await screen.findByTestId("message-1");
    expect(within(row(1)).getByRole("link", { name: "Atlas" })).toHaveAttribute(
      "href",
      "/contacts/a1",
    );
    expect(within(row(1)).getByRole("link", { name: "Open the profile of Atlas" })).toHaveAttribute(
      "href",
      "/contacts/a1",
    );
  });

  it("links a person of a bridged chat to their profile (PDM-03)", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        chat(1, {
          author_name: "Lia Costa",
          author: lia,
          sender_name: "lia",
          chat: { id: "c1", title: "Front desk", platform: "whatsapp", kind: "group" },
        }),
      ]),
    });
    renderTimeline();
    await screen.findByTestId("message-1");
    expect(within(row(1)).getByRole("link", { name: "Lia Costa" })).toHaveAttribute(
      "href",
      "/contacts/p1",
    );
    // PDM-04: the timeline never offers a DM with a person from here (DM-05 has no route).
    expect(within(row(1)).queryByText("Message privately")).toBeNull();
    expect(within(row(1)).queryByRole("menuitem")).toBeNull();
  });

  it("shows a user's avatar and name without a kind badge, and 'You' on the viewer's own (PROF-09)", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        makeMessage({ seq: 1, author: ana, author_name: "Ana Souza", text: "mine" }),
        makeMessage({
          seq: 2,
          author_id: "u2",
          author_name: "Bob Lima",
          author: makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" }),
          text: "theirs",
        }),
      ]),
    });
    renderTimeline();
    await screen.findByText("theirs");
    expect(within(row(1)).getByText("You")).toBeInTheDocument();
    expect(within(row(1)).getByTestId("intelligence-avatar")).toHaveTextContent("AS");
    expect(within(row(2)).getByTestId("intelligence-avatar")).toHaveTextContent("BL");
    expect(within(row(2)).queryByTestId("kind-badge")).toBeNull();
  });

  it("shows the name an owner set for the AI in the room, not its profile name (GRP-04)", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        makeMessage({
          seq: 1,
          author_kind: "agent",
          author_id: "a1",
          author_name: "Release captain",
          author: { ...atlas, display_name: "Release captain" },
        }),
      ]),
    });
    renderTimeline();
    await screen.findByTestId("message-1");
    expect(within(row(1)).getByRole("link", { name: "Release captain" })).toBeInTheDocument();
    expect(within(row(1)).getByTestId("intelligence-avatar")).toHaveTextContent("RC");
  });

  it("shows the person of a bridged chat with their avatar, the chat and the platform once (GRP-03)", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        chat(1, {
          author_name: "Lia Costa",
          author: lia,
          sender_name: "lia",
          chat: { id: "c1", title: "Front desk", platform: "whatsapp", kind: "group" },
        }),
      ]),
    });
    renderTimeline();
    await screen.findByTestId("message-1");
    expect(within(row(1)).getByTestId("intelligence-avatar")).toHaveTextContent("LC");
    expect(within(row(1)).getByText("Lia Costa")).toBeInTheDocument();
    expect(within(row(1)).getByText("via Front desk")).toBeInTheDocument();
    expect(within(row(1)).getAllByText("WhatsApp")).toHaveLength(1);
    expect(within(row(1)).getByText("Untrusted")).toBeInTheDocument();
  });

  it("shows the account's own avatar and name for a message typed on a linked platform account (PEOP-02)", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        chat(1, {
          author_name: "Ana Souza",
          author: ana,
          author_user_id: ME,
          sender_name: "Ana (phone)",
          chat: { id: "c1", title: "Front desk", platform: "whatsapp", kind: "group" },
        }),
      ]),
    });
    renderTimeline();
    await screen.findByTestId("message-1");
    expect(within(row(1)).getByTestId("intelligence-avatar")).toHaveTextContent("AS");
    expect(within(row(1)).getByText("You")).toBeInTheDocument();
    expect(within(row(1)).getByText("Trusted")).toBeInTheDocument();
    expect(within(row(1)).queryByText("Ana (phone)")).toBeNull();
  });

  it("keeps the chat tile and the sender's name for a chat sender with no profile", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        chat(1, {
          author: { id: "", kind: "", role: "", display_name: "Ops on call", handle: "" } as never,
        }),
      ]),
    });
    renderTimeline();
    await screen.findByTestId("message-1");
    expect(within(row(1)).queryByTestId("intelligence-avatar")).toBeNull();
    expect(within(row(1)).getByText("Diego")).toBeInTheDocument();
    expect(within(row(1)).getByText("Chat")).toBeInTheDocument();
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

  it("keeps the messages on screen when an older page fails to load", async () => {
    // A refresh that fails keeps what is shown, as the conversation list does: only a timeline
    // that never loaded is an error.
    const { calls } = mockApi({
      "GET /rooms/r1/messages": (url) =>
        url.searchParams.get("before_seq") === "51"
          ? json(500, { error: { code: "internal", message: "internal" } })
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
    await new Promise((done) => setTimeout(done, 100));

    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByTestId("message-51")).toBeInTheDocument();
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
    expect(screen.getByText(/Add members from Members/)).toBeInTheDocument();
  });
});

/** Everything written to an element's `scrollTop`, to tell a timeline that moved from one that stayed. */
function spyOnScrollTopWrites() {
  const writes: { target: Element; value: number }[] = [];
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
  return {
    writes,
    restore: () => Object.defineProperty(Element.prototype, "scrollTop", owner),
  };
}

/** The observers that watch one element, as a browser's ResizeObserver would call them. */
const resizeObservers: { callback: ResizeObserverCallback; observed: Element[] }[] = [];
class RecordingResizeObserver {
  readonly entry: { callback: ResizeObserverCallback; observed: Element[] };
  constructor(callback: ResizeObserverCallback) {
    this.entry = { callback, observed: [] };
    resizeObservers.push(this.entry);
  }
  observe(element: Element) {
    this.entry.observed.push(element);
  }
  unobserve() {}
  disconnect() {
    this.entry.observed = [];
  }
}
const resize = (el: HTMLElement, height: number) => {
  Object.defineProperty(el, "clientHeight", { configurable: true, value: height });
  act(() => {
    for (const { callback, observed } of resizeObservers) {
      if (observed.includes(el)) callback([], undefined as unknown as ResizeObserver);
    }
  });
};

describe("the timeline follows the visible area (KBD-02, KBD-06)", () => {
  const twenty = () => page(Array.from({ length: 20 }, (_, i) => makeMessage({ seq: i + 1 })));
  let spy: ReturnType<typeof spyOnScrollTopWrites>;

  beforeEach(() => {
    resizeObservers.length = 0;
    vi.stubGlobal("ResizeObserver", RecordingResizeObserver);
    spy = spyOnScrollTopWrites();
  });
  afterEach(() => spy.restore());

  it("goes back to the newest message when the keyboard takes room from it", async () => {
    mockApi({ "GET /rooms/r1/messages": twenty() });
    renderTimeline();
    const log = await screen.findByRole("log");
    await screen.findByTestId("message-20");

    spy.writes.length = 0;
    resize(log, 318);
    expect(spy.writes.filter((w) => w.target === log).length).toBeGreaterThan(0);
  });

  it("keeps a reader who scrolled up where they are when the box changes height", async () => {
    mockApi({ "GET /rooms/r1/messages": twenty() });
    renderTimeline();
    const log = await screen.findByRole("log");
    await screen.findByTestId("message-20");
    // Far from the newest message: the box is 800 px tall and 5000 px of messages lie below.
    Object.defineProperty(log, "scrollHeight", { configurable: true, value: 5000 });
    log.scrollTop = 1000;
    fireEvent.scroll(log);
    await screen.findByRole("button", { name: "Jump to latest" });

    spy.writes.length = 0;
    resize(log, 318);
    expect(spy.writes.filter((w) => w.target === log)).toEqual([]);
  });

  it("offers Jump to latest in a strip of its own, never over the messages or the composer", async () => {
    mockApi({ "GET /rooms/r1/messages": twenty() });
    renderTimeline();
    const log = await screen.findByRole("log");
    await screen.findByTestId("message-20");
    expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();

    Object.defineProperty(log, "scrollHeight", { configurable: true, value: 5000 });
    log.scrollTop = 1000;
    fireEvent.scroll(log);
    const jump = await screen.findByRole("button", { name: "Jump to latest" });

    // Outside the scrolling box, and in the flow after it: nothing is laid over a message.
    expect(log.contains(jump)).toBe(false);
    expect(jump.closest(".absolute, .fixed")).toBeNull();
    expect(log.compareDocumentPosition(jump) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("takes the strip away once the newest message is back in view", async () => {
    mockApi({ "GET /rooms/r1/messages": twenty() });
    renderTimeline();
    const log = await screen.findByRole("log");
    await screen.findByTestId("message-20");
    Object.defineProperty(log, "scrollHeight", { configurable: true, value: 5000 });
    log.scrollTop = 1000;
    fireEvent.scroll(log);
    const jump = await screen.findByRole("button", { name: "Jump to latest" });

    fireEvent.click(jump);
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull(),
    );
  });
});

describe("audio attachments (VOI-07)", () => {
  const recording = {
    id: "v1",
    name: "voice-message-2026-10-24-140509.webm",
    mime: "audio/webm;codecs=opus",
    size: 41_230,
    status: "stored" as const,
  };

  it("plays a recording inline, in a player instead of a download link", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([makeMessage({ seq: 1, text: "", attachments: [recording] })]),
    });
    renderTimeline();
    const player = await screen.findByRole("group", { name: "Voice message" });
    expect(within(player).getByRole("button", { name: "Play" })).toBeInTheDocument();
    expect(within(player).getByRole("slider", { name: "Seek" })).toBeInTheDocument();
    expect(player.closest("article")?.querySelector("audio")).toHaveAttribute(
      "src",
      "/api/v1/attachments/v1",
    );
    expect(screen.queryByRole("link", { name: /voice-message/ })).toBeNull();
  });

  it("leaves room above a grouped recording for the time that shows on hover, so the player's own time stays readable", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        makeMessage({ seq: 1, text: "listen to this" }),
        makeMessage({ seq: 2, text: "", attachments: [recording] }),
        makeMessage({ seq: 3, text: "and this", attachments: [{ ...recording, id: "v2" }] }),
      ]),
    });
    renderTimeline();
    const players = await screen.findAllByRole("group", { name: "Voice message" });
    const list = (n: number) => players[n]?.closest("ul");
    // Grouped under the first message and with no text of its own: the time sits above the player.
    expect(list(0)).toHaveClass("mt-5");
    // With text above it, the attachments follow the text as usual.
    expect(list(1)).not.toHaveClass("mt-5");
  });

  it("plays any audio file, named by the file, and leaves other files as downloads", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        makeMessage({
          seq: 1,
          attachments: [
            { id: "a1", name: "song.mp3", mime: "audio/mpeg", size: 3_000_000, status: "stored" },
            { id: "a2", name: "notes.pdf", mime: "application/pdf", size: 9_000, status: "stored" },
          ],
        }),
      ]),
    });
    renderTimeline();
    expect(await screen.findByRole("group", { name: "song.mp3" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /notes\.pdf/ })).toHaveAttribute("download");
  });

  it("shows audio that was not stored as the missing file it is, with no player", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        makeMessage({
          seq: 1,
          attachments: [{ ...recording, status: "too_large" as const }],
        }),
      ]),
    });
    renderTimeline();
    await screen.findByText(/voice-message-2026-10-24-140509\.webm/);
    expect(screen.queryByRole("group", { name: "Voice message" })).toBeNull();
    expect(screen.queryByRole("slider")).toBeNull();
  });
});

describe("edited and deleted messages (EDT-10, DLT-10)", () => {
  const bobRef = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
  const bob = (seq: number, over: Partial<Message> = {}) =>
    makeMessage({
      seq,
      author_id: "u2",
      author_name: "Bob Lima",
      author: bobRef,
      ...over,
    });
  const deletedAt = "2026-10-19T14:00:00Z";

  function renderWith(
    props: {
      canModerate?: boolean;
      onEdit?: (m: Message) => void;
      onReply?: (m: Message) => void;
    } = {},
  ) {
    return renderWithRouter(
      <MessageTimeline
        roomId="r1"
        meId={ME}
        members={[]}
        onEdit={props.onEdit}
        onReply={props.onReply}
        canModerate={props.canModerate}
      />,
    );
  }
  const actionsOf = (seq: number) =>
    within(row(seq)).queryByRole("button", { name: /^Actions for the message from / });

  /** What a row's menu offers, opened with its "⋯" and closed again; null where the row has no "⋯". */
  async function itemsOf(user: ReturnType<typeof userEvent.setup>, seq: number) {
    const more = actionsOf(seq);
    if (!more) return null;
    await user.click(more);
    const items = within(await screen.findByRole("menu"))
      .getAllByRole("menuitem")
      .map((item) => item.textContent);
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    return items;
  }

  it("marks an edited message 'edited' next to its time, and only that one", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        bob(1, { text: "plain" }),
        bob(2, { text: "fixed", edited_at: "2026-10-19T12:30:00Z" }),
      ]),
    });
    renderWith();
    await screen.findByText("fixed");
    expect(within(row(2)).getByText("edited")).toBeInTheDocument();
    expect(within(row(2)).getByText("fixed")).toBeInTheDocument();
    expect(within(row(1)).queryByText("edited")).toBeNull();
    // The marker says when, for whoever hovers it or reads it aloud.
    expect(within(row(2)).getByText("edited").closest("[title]")).toHaveAttribute(
      "title",
      expect.stringContaining("Edited"),
    );
  });

  it("shows a deleted message as 'Message deleted' in its place, with its author and time and nothing of its content", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        bob(1, { text: "before" }),
        // The placeholder never has text; a cached copy that still had some must not show it.
        bob(2, {
          text: "must not show",
          deleted_at: deletedAt,
          attachments: [
            { id: "f1", name: "secret.png", mime: "image/png", size: 4, status: "stored" },
          ],
        }),
        makeMessage({
          seq: 3,
          author_kind: "agent",
          author_id: "a1",
          author_name: "release-bot",
          text: "**bold** words",
          deleted_at: deletedAt,
        }),
        bob(4, { text: "after" }),
      ]),
    });
    renderWith();
    await screen.findByText("after");

    expect(screen.getAllByTestId(/^message-\d+$/).map((el) => el.dataset.testid)).toEqual([
      "message-1",
      "message-2",
      "message-3",
      "message-4",
    ]);
    for (const seq of [2, 3]) {
      expect(within(row(seq)).getByText("Message deleted")).toBeInTheDocument();
      expect(within(row(seq)).queryByText("must not show")).toBeNull();
      expect(within(row(seq)).queryByText(/words/)).toBeNull();
      expect(within(row(seq)).queryByRole("img")).toBeNull();
      expect(within(row(seq)).queryByRole("button", { name: /secret/ })).toBeNull();
    }
    expect(within(row(2)).getByText("Bob Lima")).toBeInTheDocument();
    expect(within(row(2)).getByRole("time")).toBeInTheDocument();
    expect(within(row(3)).getByText("release-bot")).toBeInTheDocument();
    expect(within(row(2)).queryByText("edited")).toBeNull();
  });

  it("quotes the placeholder, not the text, in a reply to a deleted message", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        bob(1, { text: "gone", deleted_at: deletedAt }),
        makeMessage({ seq: 2, text: "I disagree", reply_to: "m-r1-1" }),
      ]),
    });
    renderWith();
    await screen.findByText("I disagree");
    const quote = within(row(2)).getByTestId("reply-context");
    expect(quote).toHaveTextContent("Message deleted");
    expect(quote).not.toHaveTextContent("gone");
  });

  it("gives Edit and Delete to the viewer's own messages, Delete alone to a manager on others', and nothing on notices or deleted ones", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        makeMessage({ seq: 1, text: "mine" }),
        bob(2, { text: "bob's" }),
        makeMessage({
          seq: 3,
          author_kind: "system",
          author_id: undefined,
          author_name: "",
          text: "paused",
        }),
        bob(4, { text: "bob's gone", deleted_at: deletedAt }),
      ]),
    });
    const user = userEvent.setup();
    const onReply = vi.fn();
    const onEdit = vi.fn();
    const first = renderWith({ onReply, onEdit });
    await screen.findByText("bob's");
    expect(await itemsOf(user, 1)).toEqual(["Reply", "Copy text", "Edit", "Delete"]);
    expect(await itemsOf(user, 2)).toEqual(["Reply", "Copy text"]);
    expect(await itemsOf(user, 3)).toBeNull();
    expect(await itemsOf(user, 4)).toBeNull();
    first.unmount();

    renderWith({ onReply, onEdit, canModerate: true });
    await screen.findByText("bob's");
    expect(await itemsOf(user, 1)).toEqual(["Reply", "Copy text", "Edit", "Delete"]);
    expect(await itemsOf(user, 2)).toEqual(["Reply", "Copy text", "Delete"]);
    expect(await itemsOf(user, 3)).toBeNull();
    expect(await itemsOf(user, 4)).toBeNull();
  });

  it("offers neither Edit nor Delete in a conversation that is history", async () => {
    mockApi({ "GET /rooms/r1/messages": page([makeMessage({ seq: 1, text: "mine" })]) });
    const user = userEvent.setup();
    renderWithRouter(
      <MessageTimeline
        roomId="r1"
        meId={ME}
        members={[]}
        onReply={vi.fn()}
        onEdit={vi.fn()}
        canModerate
        frozen
      />,
    );
    await screen.findByText("mine");
    expect(await itemsOf(user, 1)).toEqual(["Copy text"]);
  });

  it("hands the message to the composer when Edit is chosen", async () => {
    mockApi({ "GET /rooms/r1/messages": page([makeMessage({ seq: 1, text: "helo" })]) });
    const onEdit = vi.fn();
    const user = userEvent.setup();
    renderWith({ onEdit });
    await screen.findByText("helo");
    await user.click(actionsOf(1) as HTMLElement);
    await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
    await waitFor(() =>
      expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({ id: "m-r1-1", text: "helo" })),
    );
    expect(onEdit).toHaveBeenCalledTimes(1);
  });

  it("follows a live edit and a live delete in place (EDT-06, DLT-06)", async () => {
    mockApi({
      "GET /rooms/r1/messages": page([
        bob(1, { text: "first" }),
        bob(2, { text: "helo wrld" }),
        bob(3, { text: "third" }),
      ]),
    });
    const { queryClient } = renderWith();
    await screen.findByText("helo wrld");

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message_updated",
        data: bob(2, { text: "hello world", edited_at: "2026-10-19T12:30:00Z" }),
      }),
    );
    await screen.findByText("hello world");
    expect(within(row(2)).getByText("edited")).toBeInTheDocument();
    expect(screen.queryByText("helo wrld")).toBeNull();

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message_updated",
        data: bob(2, { text: "", deleted_at: deletedAt }),
      }),
    );
    await waitFor(() => expect(within(row(2)).getByText("Message deleted")).toBeInTheDocument());
    expect(screen.queryByText("hello world")).toBeNull();
    expect(within(row(2)).queryByText("edited")).toBeNull();
    expect(screen.getAllByTestId(/^message-\d+$/).map((el) => el.dataset.testid)).toEqual([
      "message-1",
      "message-2",
      "message-3",
    ]);
  });

  it("says 'Mensagem apagada' and 'editada' in Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    mockApi({
      "GET /rooms/r1/messages": page([
        bob(1, { text: "ok", edited_at: "2026-10-19T12:30:00Z" }),
        bob(2, { text: "", deleted_at: deletedAt }),
      ]),
    });
    renderWith();
    await screen.findByText("ok");
    expect(within(row(1)).getByText("editada")).toBeInTheDocument();
    expect(within(row(2)).getByText("Mensagem apagada")).toBeInTheDocument();
  });
});
