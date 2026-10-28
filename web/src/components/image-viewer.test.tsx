import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { collectImages } from "@/components/image-viewer";
import { MessageTimeline } from "@/components/message-timeline";
import { useLiveStore } from "@/lib/live/store";
import type { Attachment, Message } from "@/shared/api/types.gen";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMessage } from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { stubViewport } from "@/test/media";
import { renderWithRouter } from "@/test/render";

/*
 * The image viewer (IMG-01..IMG-06): an image attachment in the timeline opens in a dialog inside
 * the app, the arrows walk the conversation's other images and closing hands focus back to the
 * image. Every test goes through the timeline, the way a user reaches the viewer.
 */

const image = (id: string, name: string, over: Partial<Attachment> = {}): Attachment => ({
  id,
  name,
  mime: "image/png",
  size: 5_000,
  status: "stored",
  ...over,
});

const notes = image("d1", "notes.txt", { mime: "text/plain", size: 90 });
const logo = image("s1", "logo.svg", { mime: "image/svg+xml", size: 900 });
const tooBig = image("t1", "huge.png", { status: "too_large", size: 9e9 });

// Four viewable pictures (one, two, three, five) among files that are not: a document, an SVG
// (never shown inline) and a picture the server did not store.
const conversation: Message[] = [
  makeMessage({ seq: 1, text: "first", attachments: [image("f1", "one.png")] }),
  makeMessage({ seq: 2, text: "notes", attachments: [notes] }),
  makeMessage({
    seq: 3,
    text: "two",
    attachments: [image("f2", "two.png"), image("f3", "three.webp", { mime: "image/webp" })],
  }),
  makeMessage({ seq: 4, text: "vector", attachments: [logo] }),
  makeMessage({ seq: 5, text: "gone", attachments: [tooBig] }),
  makeMessage({
    seq: 6,
    text: "last",
    attachments: [image("f5", "five.gif", { mime: "image/gif" })],
  }),
];

function renderTimeline(messages: Message[] = conversation) {
  // Newest first: the client must not rely on the server's order.
  mockApi({
    "GET /rooms/r1/messages": json(200, { items: [...messages].reverse(), has_more: false }),
  });
  return renderWithRouter(<MessageTimeline roomId="r1" meId="u1" members={[]} />);
}

const thumbnail = (name: string) => screen.findByRole("button", { name: `Open ${name}` });
const openViewer = async (name: string, user = userEvent.setup()) => {
  await user.click(await thumbnail(name));
  return screen.findByRole("dialog", { name });
};

stubLayout();
beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

describe("collecting the conversation's images", () => {
  it("lists the stored PNG, JPEG, GIF and WebP attachments in message order", () => {
    expect(collectImages(conversation).map((a) => a.id)).toEqual(["f1", "f2", "f3", "f5"]);
  });

  it("leaves out files the server does not show inline and files it did not store", () => {
    const only = [notes, logo, tooBig].map((a, i) => makeMessage({ seq: i + 1, attachments: [a] }));
    expect(collectImages(only)).toEqual([]);
  });
});

describe("opening an image (IMG-01, IMG-02)", () => {
  it("opens the image in a dialog inside the app, not in a new tab", async () => {
    const open = vi.fn();
    vi.stubGlobal("open", open);
    renderTimeline();
    await thumbnail("one.png");
    // Nothing in the timeline is a link that leaves the app.
    expect(document.querySelector('a[target="_blank"]')).toBeNull();

    const dialog = await openViewer("one.png");
    expect(within(dialog).getByRole("img", { name: "one.png" })).toHaveAttribute(
      "src",
      "/api/v1/attachments/f1",
    );
    expect(open).not.toHaveBeenCalled();
  });

  it("opens with the keyboard too", async () => {
    renderTimeline();
    const user = userEvent.setup();
    (await thumbnail("one.png")).focus();
    await user.keyboard("{Enter}");
    expect(await screen.findByRole("dialog", { name: "one.png" })).toBeInTheDocument();
  });

  it("shows the file name, the position among the images, Download and Open original", async () => {
    renderTimeline();
    const dialog = await openViewer("two.png");

    expect(within(dialog).getByRole("heading", { name: "two.png" })).toBeInTheDocument();
    expect(within(dialog).getByText("2 of 4")).toBeInTheDocument();
    const download = within(dialog).getByRole("link", { name: "Download" });
    expect(download).toHaveAttribute("href", "/api/v1/attachments/f2");
    expect(download).toHaveAttribute("download", "two.png");
    const original = within(dialog).getByRole("link", { name: "Open original" });
    expect(original).toHaveAttribute("href", "/api/v1/attachments/f2");
    expect(original).toHaveAttribute("target", "_blank");
    expect(original.getAttribute("rel")).toContain("noopener");
  });

  it("keeps files that are not images as plain downloads and never opens them in the viewer", async () => {
    renderTimeline();
    await thumbnail("one.png");
    expect(screen.getByRole("link", { name: /notes\.txt/ })).toHaveAttribute(
      "download",
      "notes.txt",
    );
    expect(screen.getByRole("link", { name: /logo\.svg/ })).toHaveAttribute("download", "logo.svg");
    expect(screen.queryByRole("button", { name: "Open logo.svg" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Open huge.png" })).toBeNull();
  });

  it("says so when the image cannot be loaded, and keeps the actions", async () => {
    renderTimeline();
    const dialog = await openViewer("one.png");
    fireEvent.error(within(dialog).getByRole("img", { name: "one.png" }));

    expect(await within(dialog).findByText("Couldn’t load this image.")).toBeInTheDocument();
    expect(within(dialog).getByRole("link", { name: "Download" })).toBeInTheDocument();
    expect(within(dialog).getByRole("link", { name: "Open original" })).toBeInTheDocument();
  });
});

describe("closing the viewer (IMG-03)", () => {
  it("closes on Escape and gives focus back to the image", async () => {
    renderTimeline();
    const user = userEvent.setup();
    await openViewer("one.png", user);

    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(async () => expect(await thumbnail("one.png")).toHaveFocus());
  });

  it("closes with the close button", async () => {
    renderTimeline();
    const user = userEvent.setup();
    const dialog = await openViewer("one.png", user);

    await user.click(within(dialog).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(async () => expect(await thumbnail("one.png")).toHaveFocus());
  });

  it("closes with a click on the backdrop, not with a click on the image", async () => {
    renderTimeline();
    const user = userEvent.setup();
    const dialog = await openViewer("one.png", user);

    const picture = within(dialog).getByRole("img", { name: "one.png" });
    await user.click(picture);
    fireEvent.click(picture);
    expect(screen.getByRole("dialog", { name: "one.png" })).toBeInTheDocument();

    await user.click(within(dialog).getByTestId("image-viewer-backdrop"));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(async () => expect(await thumbnail("one.png")).toHaveFocus());
  });

  it("gives focus back to the image it was showing when it closed, not to the one that opened it", async () => {
    renderTimeline();
    const user = userEvent.setup();
    await openViewer("one.png", user);
    await user.keyboard("{ArrowRight}");
    expect(await screen.findByRole("dialog", { name: "two.png" })).toBeInTheDocument();

    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(async () => expect(await thumbnail("two.png")).toHaveFocus());
  });
});

describe("moving through the conversation's images (IMG-04)", () => {
  it("walks the images with the arrow keys, oldest first, and stops at both ends", async () => {
    renderTimeline();
    const user = userEvent.setup();
    const dialog = await openViewer("one.png", user);
    expect(within(dialog).getByText("1 of 4")).toBeInTheDocument();

    await user.keyboard("{ArrowLeft}");
    expect(within(dialog).getByText("1 of 4")).toBeInTheDocument();

    // The document, the SVG and the unstored picture are skipped.
    for (const [name, position] of [
      ["two.png", "2 of 4"],
      ["three.webp", "3 of 4"],
      ["five.gif", "4 of 4"],
    ] as const) {
      await user.keyboard("{ArrowRight}");
      const next = await screen.findByRole("dialog", { name });
      expect(within(next).getByText(position)).toBeInTheDocument();
      expect(within(next).getByRole("img", { name })).toBeInTheDocument();
    }

    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("dialog", { name: "five.gif" })).toBeInTheDocument();

    await user.keyboard("{ArrowLeft}");
    expect(await screen.findByRole("dialog", { name: "three.webp" })).toBeInTheDocument();
  });

  it("walks them with Previous image and Next image, which stop being active at the ends", async () => {
    renderTimeline();
    const user = userEvent.setup();
    const dialog = await openViewer("one.png", user);
    const previous = within(dialog).getByRole("button", { name: "Previous image" });
    const next = within(dialog).getByRole("button", { name: "Next image" });
    expect(previous).toBeDisabled();
    expect(next).toBeEnabled();

    await user.click(next);
    expect(await screen.findByRole("dialog", { name: "two.png" })).toBeInTheDocument();
    expect(previous).toBeEnabled();
    await user.click(previous);
    expect(await screen.findByRole("dialog", { name: "one.png" })).toBeInTheDocument();

    await user.click(next);
    await user.click(next);
    await user.click(next);
    expect(await screen.findByRole("dialog", { name: "five.gif" })).toBeInTheDocument();
    expect(next).toBeDisabled();
  });

  it("walks them with a horizontal swipe on a touch screen", async () => {
    renderTimeline();
    const dialog = await openViewer("two.png");
    const stage = within(dialog).getByTestId("image-viewer-backdrop");
    const swipe = (from: [number, number], to: [number, number], pointerType = "touch") => {
      fireEvent.pointerDown(stage, { pointerType, clientX: from[0], clientY: from[1] });
      fireEvent.pointerUp(stage, { pointerType, clientX: to[0], clientY: to[1] });
    };

    // Dragging the picture to the left brings the next one in.
    swipe([300, 300], [120, 310]);
    expect(await screen.findByRole("dialog", { name: "three.webp" })).toBeInTheDocument();
    swipe([100, 300], [260, 290]);
    expect(await screen.findByRole("dialog", { name: "two.png" })).toBeInTheDocument();

    // A short drag, a mostly vertical one and a mouse drag are not swipes.
    swipe([300, 300], [270, 300]);
    swipe([200, 100], [120, 400]);
    swipe([300, 300], [100, 300], "mouse");
    expect(screen.getByRole("dialog", { name: "two.png" })).toBeInTheDocument();
  });

  it("offers no Previous or Next for a single image, and the arrows do nothing", async () => {
    renderTimeline([makeMessage({ seq: 1, attachments: [image("f1", "one.png")] })]);
    const user = userEvent.setup();
    const dialog = await openViewer("one.png", user);

    expect(within(dialog).queryByRole("button", { name: "Previous image" })).toBeNull();
    expect(within(dialog).queryByRole("button", { name: "Next image" })).toBeNull();
    expect(within(dialog).getByText("1 of 1")).toBeInTheDocument();
    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("dialog", { name: "one.png" })).toBeInTheDocument();
  });
});

describe("older images that are not loaded yet (IMG-04)", () => {
  // The timeline loads the newest page first; what it has not loaded cannot be walked to, so the
  // viewer asks for it while it sits on the oldest image it has, and says there may be more.
  const newest = [
    makeMessage({ seq: 4, text: "newest", attachments: [image("n4", "four.png")] }),
    makeMessage({ seq: 3, text: "newer", attachments: [image("n3", "three.png")] }),
  ];
  const pages = (older: Record<string, ReturnType<typeof json>>) =>
    mockApi({
      "GET /rooms/r1/messages": (url) => {
        const before = url.searchParams.get("before_seq");
        return before === null
          ? json(200, { items: newest, has_more: true })
          : (older[before] ?? json(200, { items: [], has_more: false }));
      },
    });
  const olderCalls = (calls: ReturnType<typeof mockApi>["calls"]) =>
    calls.filter((c) => c.path === "/rooms/r1/messages" && c.url.searchParams.has("before_seq"));
  const renderNewest = () =>
    renderWithRouter(<MessageTimeline roomId="r1" meId="u1" members={[]} />);

  it("says there may be more images while older messages are not loaded", async () => {
    const { calls } = pages({});
    renderNewest();
    const dialog = await openViewer("four.png");

    expect(within(dialog).getByText("2 of 2+")).toBeInTheDocument();
    // Not on the oldest image: nothing older is asked for.
    expect(olderCalls(calls)).toHaveLength(0);
  });

  it("loads the older page when the viewer is on the oldest image it has, and walks into it", async () => {
    const { calls } = pages({
      "3": json(200, {
        items: [
          makeMessage({ seq: 2, text: "chat" }),
          makeMessage({ seq: 1, text: "old", attachments: [image("o1", "one.png")] }),
        ],
        has_more: false,
      }),
    });
    const user = userEvent.setup();
    renderNewest();
    const dialog = await openViewer("three.png");

    await waitFor(() => expect(olderCalls(calls)).toHaveLength(1));
    expect(olderCalls(calls)[0]?.url.searchParams.get("before_seq")).toBe("3");
    // The older image joined the list in front of this one, and there is nothing older still.
    expect(await within(dialog).findByText("2 of 3")).toBeInTheDocument();
    const previous = within(dialog).getByRole("button", { name: "Previous image" });
    expect(previous).toBeEnabled();

    await user.keyboard("{ArrowLeft}");
    const first = await screen.findByRole("dialog", { name: "one.png" });
    expect(within(first).getByText("1 of 3")).toBeInTheDocument();
    expect(within(first).getByRole("button", { name: "Previous image" })).toBeDisabled();
  });

  it("keeps loading while the older pages hold no image, and stops at the start of the history", async () => {
    const { calls } = pages({
      "3": json(200, {
        items: [makeMessage({ seq: 2, text: "no picture here" })],
        has_more: true,
      }),
      "2": json(200, {
        items: [makeMessage({ seq: 1, text: "old", attachments: [image("o1", "one.png")] })],
        has_more: false,
      }),
    });
    renderNewest();
    const dialog = await openViewer("three.png");

    expect(await within(dialog).findByText("2 of 3")).toBeInTheDocument();
    expect(olderCalls(calls).map((c) => c.url.searchParams.get("before_seq"))).toEqual(["3", "2"]);
  });

  it("stops asking when asking fails, instead of asking again and again", async () => {
    const { calls } = pages({ "3": apiError(500, "internal") });
    renderNewest();
    const dialog = await openViewer("three.png");

    await waitFor(() => expect(olderCalls(calls)).toHaveLength(1));
    await new Promise((done) => setTimeout(done, 300));
    expect(olderCalls(calls)).toHaveLength(1);
    // What is loaded can still be walked.
    expect(within(dialog).getByText("1 of 2+")).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Next image" })).toBeEnabled();
  });
});

describe("focus and announcements (IMG-05)", () => {
  it("is a modal dialog named after the image, with a description of the arrows", async () => {
    renderTimeline();
    const dialog = await openViewer("one.png");

    expect(dialog).toHaveAccessibleDescription(/left and right arrow/i);
    // The rest of the page is out of reach while it is open.
    expect(screen.queryByRole("button", { name: "Open two.png" })).toBeNull();
  });

  it("puts focus inside when it opens and keeps it there while tabbing both ways", async () => {
    renderTimeline();
    const user = userEvent.setup();
    const dialog = await openViewer("one.png", user);
    expect(dialog).toContainElement(document.activeElement as HTMLElement);

    for (let i = 0; i < 9; i += 1) {
      await user.tab();
      expect(dialog).toContainElement(document.activeElement as HTMLElement);
    }
    for (let i = 0; i < 9; i += 1) {
      await user.tab({ shift: true });
      expect(dialog).toContainElement(document.activeElement as HTMLElement);
    }
  });

  it("announces the new image when it changes", async () => {
    renderTimeline();
    const user = userEvent.setup();
    const dialog = await openViewer("one.png", user);
    const announced = within(dialog).getByText("1 of 4").closest("[aria-live]");
    expect(announced).toHaveAttribute("aria-live", "polite");

    await user.keyboard("{ArrowRight}");
    const next = await screen.findByRole("dialog", { name: "two.png" });
    expect(within(next).getByText("2 of 4").closest("[aria-live]")).toHaveTextContent(
      /two\.png.*2 of 4/,
    );
  });
});

describe("a 360 px screen (IMG-06)", () => {
  it("keeps every action reachable by name when only their icons show", async () => {
    stubViewport(360);
    renderTimeline();
    const dialog = await openViewer("one.png");

    for (const name of ["Download", "Open original"]) {
      expect(within(dialog).getByRole("link", { name })).toBeInTheDocument();
    }
    for (const name of ["Close", "Next image"]) {
      expect(within(dialog).getByRole("button", { name })).toBeInTheDocument();
    }
    // The long name is cut by the layout, not by the text: the full name stays in the heading.
    expect(within(dialog).getByRole("heading", { name: "one.png" })).toHaveClass("truncate");
  });
});

describe("in Portuguese", () => {
  it("names the thumbnail, the position and every control in the app's language", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    renderTimeline();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: "Abrir two.png" }));
    const dialog = await screen.findByRole("dialog", { name: "two.png" });

    expect(within(dialog).getByText("2 de 4")).toBeInTheDocument();
    for (const name of ["Baixar", "Abrir original"]) {
      expect(within(dialog).getByRole("link", { name })).toBeInTheDocument();
    }
    for (const name of ["Fechar", "Imagem anterior", "Próxima imagem"]) {
      expect(within(dialog).getByRole("button", { name })).toBeInTheDocument();
    }
    expect(dialog).toHaveAccessibleDescription(/setas esquerda e direita/);
  });
});
