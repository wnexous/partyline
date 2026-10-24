import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RoomFiles } from "@/components/room-files";
import type { RoomFile } from "@/lib/api/types.gen";
import { catchUp, handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeAI, makeIntelligence, makeMessage, makePerson } from "@/test/fixtures";
import { renderWithRouter } from "@/test/render";

function makeFile(over: Partial<RoomFile> = {}): RoomFile {
  return {
    id: "f1",
    name: "report.pdf",
    mime: "application/pdf",
    size: 2048,
    status: "stored",
    kind: "document",
    message_id: "m-r1-4",
    message_seq: 4,
    author_kind: "user",
    author_name: "Ana",
    author: makeIntelligence({ display_name: "Ana", handle: "ana" }),
    created_at: "2026-10-19T12:00:00Z",
    ...over,
  };
}

const photo = makeFile({
  id: "f3",
  name: "whiteboard.png",
  mime: "image/png",
  size: 512_000,
  kind: "image",
  message_id: "m-r1-9",
  message_seq: 9,
  author_kind: "chat",
  author_name: "Customer",
  // A chat sender that resolved to nobody: no id, only the name that shows.
  author: { id: "", kind: "", role: "", display_name: "Customer", handle: "" } as never,
});
const report = makeFile();

const page = (items: RoomFile[], has_more = false) => json(200, { items, has_more });
const filesCalls = (calls: ReturnType<typeof mockApi>["calls"]) =>
  calls.filter((c) => c.path === "/rooms/r1/attachments");

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

describe("room files (FILES-04)", () => {
  it("lists each file with its thumbnail or icon, size, author and time, and a download link", async () => {
    mockApi({ "GET /rooms/r1/attachments": page([photo, report]) });
    renderWithRouter(<RoomFiles roomId="r1" onOpenMessage={() => {}} />);

    const list = await screen.findByRole("list", { name: "Files shared in the room" });
    const rows = within(list).getAllByRole("listitem");
    expect(rows.map((r) => r.dataset.testid)).toEqual(["file-f3", "file-f1"]);

    const img = screen.getByTestId("file-f3").querySelector("img");
    expect(img?.getAttribute("src")).toBe("/api/v1/attachments/f3");
    const photoLink = within(screen.getByTestId("file-f3")).getByRole("link", {
      name: "whiteboard.png",
    });
    expect(photoLink).toHaveAttribute("href", "/api/v1/attachments/f3");
    expect(screen.getByTestId("file-f3")).toHaveTextContent("500 KB");
    expect(screen.getByTestId("file-f3")).toHaveTextContent("Customer");

    const doc = screen.getByTestId("file-f1");
    expect(doc.querySelector("img")).toBeNull();
    expect(doc.querySelector("svg")).not.toBeNull();
    const docLink = within(doc).getByRole("link", { name: "report.pdf" });
    expect(docLink).toHaveAttribute("href", "/api/v1/attachments/f1");
    expect(docLink).toHaveAttribute("download", "report.pdf");
    expect(doc).toHaveTextContent("2 KB");
    expect(doc).toHaveTextContent("Ana");
    const time = doc.querySelector("time");
    expect(time).toHaveAttribute("dateTime", "2026-10-19T12:00:00Z");
    expect(time?.textContent).not.toBe("");
  });

  it("filters by kind with the kind query parameter", async () => {
    const { calls } = mockApi({
      "GET /rooms/r1/attachments": (url) =>
        url.searchParams.get("kind") === "image" ? page([photo]) : page([photo, report]),
    });
    const user = userEvent.setup();
    renderWithRouter(<RoomFiles roomId="r1" onOpenMessage={() => {}} />);
    await screen.findByTestId("file-f1");
    expect(filesCalls(calls)[0]?.url.searchParams.has("kind")).toBe(false);

    await user.click(screen.getByRole("radio", { name: "Images" }));
    await waitFor(() => expect(screen.queryByTestId("file-f1")).toBeNull());
    expect(screen.getByTestId("file-f3")).toBeInTheDocument();
    expect(filesCalls(calls).at(-1)?.url.searchParams.get("kind")).toBe("image");

    for (const [label, kind] of [
      ["Audio", "audio"],
      ["Video", "video"],
      ["Docs", "document"],
    ] as const) {
      await user.click(screen.getByRole("radio", { name: label }));
      await waitFor(() =>
        expect(filesCalls(calls).at(-1)?.url.searchParams.get("kind")).toBe(kind),
      );
    }
  });

  it("opens the file's message in the timeline", async () => {
    mockApi({ "GET /rooms/r1/attachments": page([photo, report]) });
    const onOpen = vi.fn();
    const user = userEvent.setup();
    renderWithRouter(<RoomFiles roomId="r1" onOpenMessage={onOpen} />);
    await user.click(
      await screen.findByRole("button", { name: "Open report.pdf in the timeline" }),
    );
    expect(onOpen).toHaveBeenCalledWith({ id: "m-r1-4", seq: 4 });
  });

  it("loads older files with the last file's id as the cursor", async () => {
    const older = makeFile({ id: "f0", name: "old-notes.txt", mime: "text/plain" });
    const { calls } = mockApi({
      "GET /rooms/r1/attachments": (url) =>
        url.searchParams.get("before") === "f1" ? page([older]) : page([photo, report], true),
    });
    const user = userEvent.setup();
    renderWithRouter(<RoomFiles roomId="r1" onOpenMessage={() => {}} />);
    await user.click(await screen.findByRole("button", { name: "Load older files" }));
    await screen.findByTestId("file-f0");
    expect(filesCalls(calls).at(-1)?.url.searchParams.get("before")).toBe("f1");
    expect(screen.queryByRole("button", { name: "Load older files" })).toBeNull();
  });

  it("explains where shared files appear when the room has none", async () => {
    mockApi({ "GET /rooms/r1/attachments": page([]) });
    renderWithRouter(<RoomFiles roomId="r1" onOpenMessage={() => {}} />);
    expect(await screen.findByText("No files yet")).toBeInTheDocument();
    expect(
      screen.getByText("Images, audio, video and documents shared in this room appear here."),
    ).toBeInTheDocument();
  });

  it("shows a load error with a retry", async () => {
    let fail = true;
    mockApi({
      "GET /rooms/r1/attachments": () => (fail ? apiError(500, "internal") : page([report])),
    });
    const user = userEvent.setup();
    renderWithRouter(<RoomFiles roomId="r1" onOpenMessage={() => {}} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn’t load the files.");
    fail = false;
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByTestId("file-f1")).toBeInTheDocument();
  });
});

describe("authors read alike (PROF-09, AD-023)", () => {
  it("names the author of a file by name alone, whether an AI, a person, a user or an unresolved sender", async () => {
    mockApi({
      "GET /rooms/r1/attachments": page([
        makeFile({
          id: "f5",
          name: "build.log",
          author_kind: "agent",
          author_name: "release-bot",
          author: makeAI({ id: "a9", display_name: "release-bot", handle: "release-bot" }),
        }),
        makeFile({
          id: "f6",
          name: "invoice.pdf",
          author_kind: "chat",
          author_name: "Lia Costa",
          author: makePerson(),
        }),
        photo,
        report,
      ]),
    });
    renderWithRouter(<RoomFiles roomId="r1" onOpenMessage={() => {}} />);

    const ai = within(await screen.findByTestId("file-f5"));
    expect(ai.getByText("release-bot")).toBeInTheDocument();
    expect(ai.queryByTestId("kind-badge")).toBeNull();
    expect(ai.queryByText(/^(AI|IA)$/)).toBeNull();
    const person = within(screen.getByTestId("file-f6"));
    expect(person.getByText("Lia Costa")).toBeInTheDocument();
    expect(person.queryByTestId("kind-badge")).toBeNull();
    expect(person.queryByText("WhatsApp")).toBeNull();
    expect(within(screen.getByTestId("file-f1")).getByText("Ana")).toBeInTheDocument();
    expect(within(screen.getByTestId("file-f1")).queryByTestId("kind-badge")).toBeNull();
    // A chat account nobody resolved keeps only the name it shows.
    expect(within(screen.getByTestId("file-f3")).getByText("Customer")).toBeInTheDocument();
    expect(within(screen.getByTestId("file-f3")).queryByTestId("kind-badge")).toBeNull();
  });
});

describe("unavailable files (FILES-06)", () => {
  it("lists files that were not stored as unavailable, without a download link", async () => {
    mockApi({
      "GET /rooms/r1/attachments": page([
        makeFile({
          id: "f8",
          name: "huge.mov",
          mime: "video/quicktime",
          kind: "video",
          status: "too_large",
        }),
        makeFile({
          id: "f9",
          name: "lost.jpg",
          mime: "image/jpeg",
          kind: "image",
          status: "unavailable",
        }),
      ]),
    });
    renderWithRouter(<RoomFiles roomId="r1" onOpenMessage={() => {}} />);
    const huge = await screen.findByTestId("file-f8");
    const lost = screen.getByTestId("file-f9");
    expect(huge).toHaveTextContent("huge.mov");
    expect(huge).toHaveTextContent("too large to store");
    expect(lost).toHaveTextContent("unavailable");
    for (const row of [huge, lost]) {
      // No link to the file; the only link left is the author's, to their profile (PRF-01).
      expect(row.querySelector("a[download]")).toBeNull();
      expect(row.querySelector('a[href^="/api/v1/attachments/"]')).toBeNull();
      expect(
        within(row)
          .getAllByRole("link")
          .map((a) => a.getAttribute("href")),
      ).toEqual(["/contacts/u1"]);
      expect(row.querySelector("img")).toBeNull();
    }
    // Still reachable in the timeline.
    expect(
      within(lost).getByRole("button", { name: "Open lost.jpg in the timeline" }),
    ).toBeEnabled();
  });
});

describe("live files (FILES-05)", () => {
  const liveMessage = makeMessage({
    id: "m-r1-12",
    seq: 12,
    author_kind: "agent",
    author_name: "release-bot",
    author: makeAI({ id: "a9", display_name: "release-bot", handle: "release-bot" }),
    created_at: "2026-10-19T12:30:00Z",
    attachments: [
      { id: "f20", name: "build.log", mime: "text/plain", size: 100, status: "stored" },
      { id: "f21", name: "chart.webp", mime: "image/webp", size: 3000, status: "stored" },
    ],
  });

  it("puts the files of a new message at the top without a reload", async () => {
    const { calls } = mockApi({ "GET /rooms/r1/attachments": page([photo, report]) });
    const { queryClient } = renderWithRouter(<RoomFiles roomId="r1" onOpenMessage={() => {}} />);
    await screen.findByTestId("file-f1");
    const fetches = filesCalls(calls).length;

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message",
        data: liveMessage,
      }),
    );
    await waitFor(() => expect(screen.getByTestId("file-f21")).toBeInTheDocument());
    const rows = within(
      screen.getByRole("list", { name: "Files shared in the room" }),
    ).getAllByRole("listitem");
    expect(rows.map((r) => r.dataset.testid)).toEqual([
      "file-f21",
      "file-f20",
      "file-f3",
      "file-f1",
    ]);
    expect(screen.getByTestId("file-f21")).toHaveTextContent("release-bot");
    expect(within(screen.getByTestId("file-f21")).queryByTestId("kind-badge")).toBeNull();
    expect(screen.getByTestId("file-f21").querySelector("img")?.getAttribute("src")).toBe(
      "/api/v1/attachments/f21",
    );
    expect(filesCalls(calls)).toHaveLength(fetches);

    // A repeated event adds nothing; messages of other rooms and without files are ignored.
    act(() => {
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message",
        data: liveMessage,
      });
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message",
        data: {
          ...liveMessage,
          id: "m-r2-1",
          room_id: "r2",
          attachments: [
            { id: "f30", name: "x.txt", mime: "text/plain", size: 1, status: "stored" },
          ],
        },
      });
    });
    expect(screen.getAllByTestId(/^file-/)).toHaveLength(4);
  });

  it("refetches the files after a reconnect, when live events may have been missed", async () => {
    const { calls } = mockApi({
      "GET /rooms/r1/attachments": () =>
        page(
          filesCalls(calls).length > 1
            ? [makeFile({ id: "f40", name: "missed.zip" }), report]
            : [report],
        ),
    });
    const { queryClient } = renderWithRouter(<RoomFiles roomId="r1" onOpenMessage={() => {}} />);
    await screen.findByTestId("file-f1");
    await act(() => catchUp(queryClient));
    expect(await screen.findByTestId("file-f40")).toBeInTheDocument();
  });

  it("adds only files of the selected kind to a filtered list", async () => {
    mockApi({
      "GET /rooms/r1/attachments": (url) =>
        url.searchParams.get("kind") === "image" ? page([photo]) : page([photo, report]),
    });
    const user = userEvent.setup();
    const { queryClient } = renderWithRouter(<RoomFiles roomId="r1" onOpenMessage={() => {}} />);
    await screen.findByTestId("file-f1");
    await user.click(screen.getByRole("radio", { name: "Images" }));
    await waitFor(() => expect(screen.queryByTestId("file-f1")).toBeNull());

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message",
        data: liveMessage,
      }),
    );
    await waitFor(() => expect(screen.getByTestId("file-f21")).toBeInTheDocument());
    expect(screen.queryByTestId("file-f20")).toBeNull();
  });
});
