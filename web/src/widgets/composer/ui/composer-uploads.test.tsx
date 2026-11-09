import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import type { Upload } from "@/shared/api/types.gen";
import { DIRECT_POST_BYTES } from "@/shared/api/uploads";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMessage } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { AppProviders, makeQueryClient, renderWithProviders } from "@/test/render";
import { Composer } from "@/widgets/composer";

/*
 * Large files in the composer (LF-01): a file over what one request carries goes up through an
 * upload session in chunks, its card shows the progress, its button cancels, and the message posts
 * with `upload_ids` once every file is up.
 */

const MB = 1 << 20;
const CHUNK = 4 * MB;

type Route = (url: URL, init: RequestInit | undefined) => Response | Promise<Response>;

/**
 * Upload session routes for mockApi, answering like the server: sessions up1, up2… in order, each
 * chunk appended where the session is. `gate` holds a chunk's answer until the test lets it go.
 */
function uploadRoutes({ files = 1, chunkSize = CHUNK, maxSize = 5 * 1024 * MB } = {}) {
  const sessions = new Map<string, Upload>();
  const gates: Array<() => void> = [];
  const state = { gated: false };
  let next = 0;
  const routes: Record<string, Route> = {
    "POST /uploads": (_url, init) => {
      const { name, mime, size } = JSON.parse(String(init?.body)) as Upload;
      next += 1;
      const upload: Upload = {
        id: `up${next}`,
        name,
        mime,
        size,
        received: 0,
        complete: false,
        chunk_size: chunkSize,
        max_size: maxSize,
        expires_at: "2026-11-10T00:00:00Z",
      };
      sessions.set(upload.id, upload);
      return json(201, upload);
    },
  };
  for (let i = 1; i <= files; i += 1) {
    const id = `up${i}`;
    routes[`GET /uploads/${id}`] = () => {
      const upload = sessions.get(id);
      return upload ? json(200, upload) : apiError(404, "not_found");
    };
    routes[`PUT /uploads/${id}`] = async (url, init) => {
      const upload = sessions.get(id);
      if (!upload) return apiError(404, "not_found");
      if (state.gated) await new Promise<void>((release) => gates.push(release));
      if (init?.signal?.aborted) throw new DOMException("aborted", "AbortError");
      upload.received =
        Number(url.searchParams.get("offset")) + ((init?.body as Blob | undefined)?.size ?? 0);
      upload.complete = upload.received === upload.size;
      return json(200, upload);
    };
  }
  /** Lets the chunk that waits go on. */
  const release = async () => {
    await waitFor(() => expect(gates.length).toBeGreaterThan(0));
    await act(async () => gates.shift()?.());
  };
  return { routes, sessions, state, release };
}

const fileOf = (name: string, size: number, type = "video/mp4") =>
  new File([new Uint8Array(size)], name, { type });

const PT_ATTACH = "Anexar arquivos";
const PT_SEND = "Enviar";

const BIG = DIRECT_POST_BYTES + 2 * MB; // 10 MB: three chunks of 4 MB

function renderComposer() {
  return renderWithProviders(<Composer roomId="r1" roomName="deploys" level="post" />);
}

const box = () => screen.getByRole("textbox", { name: "Message deploys" });
const posts = (calls: { method: string; path: string; body: unknown }[]) =>
  calls.filter((c) => c.method === "POST" && c.path === "/rooms/r1/messages");
const puts = (calls: { method: string; path: string; url: URL }[]) =>
  calls
    .filter((c) => c.method === "PUT")
    .map((c) => `${c.path}@${c.url.searchParams.get("offset")}`);

beforeEach(() => useLiveStore.setState({ locale: "en" }));

describe("large files go through upload sessions (LF-01)", () => {
  it("uploads a big file in chunks and posts the message with its upload id", async () => {
    const server = uploadRoutes();
    const { calls } = mockApi({
      ...server.routes,
      "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })),
    });
    const user = userEvent.setup();
    renderComposer();
    await user.type(box(), "the raw footage");
    await user.upload(screen.getByLabelText("Attach files"), fileOf("raw.mp4", BIG));
    await user.click(screen.getByRole("button", { name: "Send" }));

    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(puts(calls)).toEqual([
      "/uploads/up1@0",
      `/uploads/up1@${CHUNK}`,
      `/uploads/up1@${2 * CHUNK}`,
    ]);
    // A JSON post: the file is already on the server.
    expect(posts(calls)[0]?.body).toEqual({ text: "the raw footage", upload_ids: ["up1"] });
    await waitFor(() => expect(screen.queryByText("raw.mp4")).toBeNull());
    expect(box()).toHaveValue("");
  });

  it("sends every file of a message through sessions, in the order they were added", async () => {
    const server = uploadRoutes({ files: 2 });
    const { calls } = mockApi({
      ...server.routes,
      "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })),
    });
    const user = userEvent.setup();
    renderComposer();
    await user.upload(screen.getByLabelText("Attach files"), [
      fileOf("notes.txt", 10, "text/plain"),
      fileOf("raw.mp4", BIG),
    ]);
    await user.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(posts(calls)[0]?.body).toEqual({ text: "", upload_ids: ["up1", "up2"] });
    expect(server.sessions.get("up1")?.name).toBe("notes.txt");
  });

  it("shows each file's progress as a progressbar on its card while it uploads", async () => {
    const server = uploadRoutes();
    server.state.gated = true;
    const { calls } = mockApi({
      ...server.routes,
      "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })),
    });
    const user = userEvent.setup();
    renderComposer();
    await user.upload(screen.getByLabelText("Attach files"), fileOf("raw.mp4", BIG));
    await user.click(screen.getByRole("button", { name: "Send" }));

    const bar = await screen.findByRole("progressbar", { name: "Uploading raw.mp4" });
    expect(bar).toHaveAttribute("aria-valuenow", "0");
    expect(bar).toHaveAttribute("aria-valuemin", "0");
    expect(bar).toHaveAttribute("aria-valuemax", "100");
    expect(screen.getByText("0% of 10 MB")).toBeInTheDocument();
    // Send is off while the file goes up: a second tap would post it twice.
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();

    await server.release();
    await waitFor(() => expect(bar).toHaveAttribute("aria-valuenow", "40"));
    expect(screen.getByText("40% of 10 MB")).toBeInTheDocument();
    await server.release();
    await waitFor(() => expect(bar).toHaveAttribute("aria-valuenow", "80"));
    await server.release();
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    await waitFor(() => expect(screen.queryByRole("progressbar")).toBeNull());
  });

  it("keeps what is typed while a file uploads: only what the post carried leaves the draft", async () => {
    const server = uploadRoutes();
    server.state.gated = true;
    const { calls } = mockApi({
      ...server.routes,
      "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })),
    });
    const user = userEvent.setup();
    renderComposer();
    await user.type(box(), "first");
    await user.upload(screen.getByLabelText("Attach files"), fileOf("raw.mp4", BIG));
    await user.click(screen.getByRole("button", { name: "Send" }));
    await screen.findByRole("progressbar");
    await user.clear(box());
    await user.type(box(), "the next one");
    await server.release();
    await server.release();
    await server.release();
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(posts(calls)[0]?.body).toMatchObject({ text: "first" });
    await waitFor(() => expect(screen.queryByText("raw.mp4")).toBeNull());
    expect(box()).toHaveValue("the next one");
  });

  it("cancels: the card's button aborts the upload, takes the file out and posts nothing", async () => {
    const server = uploadRoutes();
    server.state.gated = true;
    const { calls } = mockApi({
      ...server.routes,
      "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })),
    });
    const user = userEvent.setup();
    renderComposer();
    await user.type(box(), "never mind");
    await user.upload(screen.getByLabelText("Attach files"), fileOf("raw.mp4", BIG));
    await user.click(screen.getByRole("button", { name: "Send" }));
    await screen.findByRole("progressbar");

    await user.click(screen.getByRole("button", { name: "Cancel the upload of raw.mp4" }));
    await server.release();
    await waitFor(() => expect(screen.queryByText("raw.mp4")).toBeNull());
    expect(screen.getByRole("status")).toHaveTextContent("Upload cancelled");
    await act(async () => new Promise((r) => setTimeout(r, 20)));
    // The chunk in flight was the last request: no next chunk and no post.
    expect(puts(calls)).toEqual(["/uploads/up1@0"]);
    expect(posts(calls)).toHaveLength(0);
    expect(screen.queryByRole("alert")).toBeNull();
    // The text stays, and the message can be sent without the file.
    expect(box()).toHaveValue("never mind");
    expect(screen.getByRole("button", { name: "Send" })).toBeEnabled();
  });

  it("goes on from where a failed upload stopped when Send is pressed again", async () => {
    const server = uploadRoutes();
    let failed = false;
    const put = server.routes["PUT /uploads/up1"] as Route;
    const { calls } = mockApi({
      ...server.routes,
      // The second chunk is refused for good the first time (a full disk), then taken.
      "PUT /uploads/up1": (url, init) => {
        if (url.searchParams.get("offset") === String(CHUNK) && !failed) {
          failed = true;
          return apiError(507, "insufficient_storage");
        }
        return put(url, init);
      },
      "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })),
    });
    const user = userEvent.setup();
    renderComposer();
    await user.upload(screen.getByLabelText("Attach files"), fileOf("raw.mp4", BIG));
    await user.click(screen.getByRole("button", { name: "Send" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The server doesn’t have enough free disk space for “raw.mp4”.",
    );
    expect(screen.getByText("raw.mp4")).toBeInTheDocument();
    expect(posts(calls)).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    // One session: the first chunk was not sent again.
    expect(calls.filter((c) => c.method === "POST" && c.path === "/uploads")).toHaveLength(1);
    expect(puts(calls)).toEqual([
      "/uploads/up1@0",
      `/uploads/up1@${CHUNK}`,
      `/uploads/up1@${CHUNK}`,
      `/uploads/up1@${2 * CHUNK}`,
    ]);
    expect(posts(calls)[0]?.body).toEqual({ text: "", upload_ids: ["up1"] });
  });

  it("sends small files through a session when the server refuses them as one request", async () => {
    const server = uploadRoutes({ chunkSize: MB });
    const { calls } = mockApi({
      ...server.routes,
      "POST /rooms/r1/messages": (_url, init) =>
        init?.body instanceof FormData
          ? apiError(413, "too_large")
          : json(201, makeMessage({ seq: 9 })),
    });
    const user = userEvent.setup();
    renderComposer();
    await user.upload(
      screen.getByLabelText("Attach files"),
      fileOf("photo.jpg", 3 * MB, "image/jpeg"),
    );
    await user.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(posts(calls)).toHaveLength(2));
    expect(posts(calls)[0]?.body).toBeInstanceOf(FormData);
    expect(posts(calls)[1]?.body).toEqual({ text: "", upload_ids: ["up1"] });
    expect(puts(calls)).toEqual(["/uploads/up1@0", `/uploads/up1@${MB}`, `/uploads/up1@${2 * MB}`]);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("keeps small files on the one-request multipart path", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer();
    await user.upload(
      screen.getByLabelText("Attach files"),
      fileOf("shot.png", 2 * MB, "image/png"),
    );
    await user.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(posts(calls)[0]?.body).toBeInstanceOf(FormData);
    expect(calls.some((c) => c.path.startsWith("/uploads"))).toBe(false);
  });
});

describe("what a refused upload says (LF-01)", () => {
  const refuse = (status: number, code: string, details: Record<string, string> = {}) =>
    mockApi({
      "POST /uploads": json(status, { error: { code, message: code, details } }),
      "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })),
    });

  const sendBig = async (attach = "Attach files", send = "Send") => {
    const user = userEvent.setup();
    renderComposer();
    await user.upload(screen.getByLabelText(attach), fileOf("movie.mkv", BIG));
    await user.click(screen.getByRole("button", { name: send }));
    return screen.findByRole("alert");
  };

  it("names the file and the server's max_size for a file over it", async () => {
    refuse(413, "file_too_large", { max_size: String(5 * 1024 * MB) });
    expect(await sendBig()).toHaveTextContent(
      "“movie.mkv” is larger than this server takes (5 GB).",
    );
    expect(screen.getByText("movie.mkv")).toBeInTheDocument();
  });

  it("says the server's disk is full (507)", async () => {
    refuse(507, "insufficient_storage");
    expect(await sendBig()).toHaveTextContent(
      "The server doesn’t have enough free disk space for “movie.mkv”. Try a smaller file, or ask an admin to free some space.",
    );
  });

  it("says too many files wait on the server (429)", async () => {
    refuse(429, "rate_limited");
    expect(await sendBig()).toHaveTextContent(/Too many files are waiting on the server/);
  });

  it("says it in Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    refuse(413, "file_too_large", { max_size: String(5 * 1024 * MB) });
    expect(await sendBig(PT_ATTACH, PT_SEND)).toHaveTextContent(
      "“movie.mkv” é maior do que este servidor aceita (5 GB).",
    );
  });

  it("says the disk is full in Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    refuse(507, "insufficient_storage");
    expect(await sendBig(PT_ATTACH, PT_SEND)).toHaveTextContent(
      "O servidor não tem espaço livre em disco suficiente para “movie.mkv”.",
    );
  });
});

describe("an upload outlives the composer (LF-01, WA-07)", () => {
  it("still shows the progress after another conversation and back, and the send ends there", async () => {
    const server = uploadRoutes();
    server.state.gated = true;
    const { calls } = mockApi({
      ...server.routes,
      "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })),
    });
    const user = userEvent.setup();
    const queryClient = makeQueryClient();
    const at = (roomId: string, roomName: string) => (
      <AppProviders queryClient={queryClient}>
        <Composer key={roomId} roomId={roomId} roomName={roomName} level="post" />
      </AppProviders>
    );
    const { rerender } = renderWithProviders(
      <Composer key="r1" roomId="r1" roomName="deploys" level="post" />,
      queryClient,
    );
    await user.upload(screen.getByLabelText("Attach files"), fileOf("raw.mp4", BIG));
    await user.click(screen.getByRole("button", { name: "Send" }));
    await screen.findByRole("progressbar");
    await server.release();

    rerender(at("r2", "alerts"));
    expect(screen.queryByRole("progressbar")).toBeNull();
    rerender(at("r1", "deploys"));
    const card = screen.getByRole("group", { name: /raw\.mp4/ });
    await waitFor(() =>
      expect(within(card).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "40"),
    );
    // The send is still going: this composer must not start a second one.
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
    await server.release();
    await server.release();
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    await waitFor(() => expect(screen.queryByText("raw.mp4")).toBeNull());
  });
});
