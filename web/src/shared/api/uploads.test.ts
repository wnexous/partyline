import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiError, json } from "@/test/fetch";
import type { Upload } from "./types.gen";
import {
  CLIENT_CHUNK_BYTES,
  DIRECT_POST_BYTES,
  fitsOneRequest,
  isUploadError,
  knownMaxFileSize,
  resetUploadLimits,
  type UploadProgress,
  uploadFile,
} from "./uploads";

const MB = 1 << 20;

/** A request the fake server saw: method, path, offset and the size of the body. */
interface Seen {
  method: string;
  path: string;
  offset?: number;
  bytes?: number;
}

type Fault = (req: Seen) => Response | "network" | undefined;

/**
 * A fake upload server behind fetch: it keeps what each session received, answers like the real one
 * (409 upload_offset for a chunk at the wrong offset) and lets a test break a request with `fault`.
 */
function fakeServer({ chunkSize = 2 * MB, maxSize = 5 * 1024 * MB } = {}) {
  const sessions = new Map<string, Upload>();
  const seen: Seen[] = [];
  const faults: Fault[] = [];
  let next = 0;
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const method = (init?.method ?? "GET").toUpperCase();
    const path = url.pathname.replace(/^\/api\/v1/, "");
    const offset = url.searchParams.has("offset")
      ? Number(url.searchParams.get("offset"))
      : undefined;
    const body = init?.body;
    const req: Seen = {
      method,
      path,
      offset,
      bytes: body instanceof Blob ? body.size : undefined,
    };
    seen.push(req);
    if (init?.signal?.aborted) throw new DOMException("aborted", "AbortError");
    for (const fault of faults) {
      const out = fault(req);
      if (out === "network") throw new TypeError("Failed to fetch");
      if (out) return out;
    }
    if (method === "POST" && path === "/uploads") {
      const { name, mime, size } = JSON.parse(String(body)) as Upload;
      if (size > maxSize) {
        return json(413, {
          error: {
            code: "file_too_large",
            message: "too big",
            details: { max_size: String(maxSize) },
          },
        });
      }
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
    }
    const id = path.replace(/^\/uploads\//, "");
    const upload = sessions.get(id);
    if (!upload) return apiError(404, "not_found");
    if (method === "GET") return json(200, upload);
    if (method === "PUT") {
      if (offset !== upload.received) {
        return json(409, {
          error: {
            code: "upload_offset",
            message: "offset",
            details: { received: String(upload.received), complete: String(upload.complete) },
          },
        });
      }
      upload.received += req.bytes ?? 0;
      upload.complete = upload.received === upload.size;
      if (upload.complete) upload.sha256 = "abc";
      return json(200, upload);
    }
    return apiError(405, "invalid");
  });
  vi.stubGlobal("fetch", fetchMock);
  const puts = () => seen.filter((s) => s.method === "PUT");
  return { sessions, seen, faults, puts, fetchMock };
}

const fileOf = (size: number, name = "video.mp4", type = "video/mp4") =>
  new File([new Uint8Array(size)], name, { type });

const noDelay = () => 0;

beforeEach(() => resetUploadLimits());
afterEach(() => vi.unstubAllGlobals());

describe("uploadFile (LF-01)", () => {
  it("creates the session, then sends the chunks in order from offset 0 until complete", async () => {
    const server = fakeServer({ chunkSize: 2 * MB });
    const file = fileOf(5 * MB + 3);
    const done = await uploadFile(file, { retryDelay: noDelay });
    expect(server.seen[0]).toMatchObject({ method: "POST", path: "/uploads" });
    expect(server.puts().map((p) => [p.offset, p.bytes])).toEqual([
      [0, 2 * MB],
      [2 * MB, 2 * MB],
      [4 * MB, MB + 3],
    ]);
    expect(done).toMatchObject({ id: "up1", complete: true, received: file.size });
  });

  it("sends the name, the type and the exact size, and application/octet-stream for a file without a type", async () => {
    const server = fakeServer();
    await uploadFile(fileOf(10, "raw.cr3", ""), { retryDelay: noDelay });
    const create = server.fetchMock.mock.calls[0]?.[1] as RequestInit;
    expect(JSON.parse(String(create.body))).toEqual({
      name: "raw.cr3",
      mime: "application/octet-stream",
      size: 10,
    });
  });

  it("never sends a chunk larger than the client's own cap, even when the server allows 50 MB", async () => {
    const server = fakeServer({ chunkSize: 50 * MB });
    await uploadFile(fileOf(CLIENT_CHUNK_BYTES + 1), { retryDelay: noDelay });
    expect(server.puts().map((p) => p.bytes)).toEqual([CLIENT_CHUNK_BYTES, 1]);
  });

  it("reports the bytes the server holds after each chunk, from 0 to the whole file", async () => {
    fakeServer({ chunkSize: 2 * MB });
    const progress: UploadProgress[] = [];
    await uploadFile(fileOf(5 * MB), { onProgress: (p) => progress.push(p), retryDelay: noDelay });
    expect(progress).toEqual([
      { sent: 0, total: 5 * MB },
      { sent: 2 * MB, total: 5 * MB },
      { sent: 4 * MB, total: 5 * MB },
      { sent: 5 * MB, total: 5 * MB },
    ]);
  });

  it("retries a chunk that failed on the network, without starting over", async () => {
    const server = fakeServer({ chunkSize: 2 * MB });
    let broken = 2;
    server.faults.push((req) =>
      req.method === "PUT" && req.offset === 2 * MB && broken-- > 0 ? "network" : undefined,
    );
    const delays: number[] = [];
    const done = await uploadFile(fileOf(5 * MB), {
      retryDelay: (attempt) => {
        delays.push(attempt);
        return 0;
      },
    });
    expect(done.complete).toBe(true);
    // One session only, and the chunk at 2 MB went three times; the first one never again.
    expect(server.seen.filter((s) => s.method === "POST")).toHaveLength(1);
    expect(server.puts().map((p) => p.offset)).toEqual([0, 2 * MB, 2 * MB, 2 * MB, 4 * MB]);
    expect(delays).toEqual([1, 2]);
  });

  it("retries a chunk the server failed with a 5xx, and asks where it is before resending", async () => {
    const server = fakeServer({ chunkSize: 2 * MB });
    let broken = 1;
    server.faults.push((req) =>
      req.method === "PUT" && req.offset === 0 && broken-- > 0
        ? apiError(502, "internal")
        : undefined,
    );
    await uploadFile(fileOf(3 * MB), { retryDelay: noDelay });
    expect(server.seen.map((s) => `${s.method} ${s.offset ?? ""}`)).toEqual([
      "POST ",
      "PUT 0",
      "GET ",
      "PUT 0",
      `PUT ${2 * MB}`,
    ]);
  });

  it("gives up after three retries of the same chunk and says which file failed", async () => {
    const server = fakeServer({ chunkSize: 2 * MB });
    server.faults.push((req) =>
      req.method === "PUT" && req.offset === 2 * MB ? "network" : undefined,
    );
    const err = await uploadFile(fileOf(5 * MB, "clip.mov"), { retryDelay: noDelay }).catch(
      (e) => e,
    );
    expect(isUploadError(err)).toBe(true);
    expect(err).toMatchObject({ status: 0, code: "unavailable", fileName: "clip.mov" });
    expect(server.puts().filter((p) => p.offset === 2 * MB)).toHaveLength(4);
  });

  it("resumes from details.received on 409 upload_offset (an answer that got lost)", async () => {
    const server = fakeServer({ chunkSize: 2 * MB });
    let lost = 1;
    // The chunk at 2 MB arrives, but its answer is lost on the way back.
    server.faults.push((req) => {
      if (req.method !== "PUT" || req.offset !== 2 * MB || lost-- <= 0) return undefined;
      const upload = server.sessions.get("up1");
      if (upload) upload.received += req.bytes ?? 0;
      return "network";
    });
    await uploadFile(fileOf(5 * MB), { retryDelay: noDelay });
    const sends = server.puts().map((p) => p.offset);
    // After the failure the GET finds 4 MB received, so the next chunk is the last one.
    expect(sends).toEqual([0, 2 * MB, 4 * MB]);
  });

  it("goes on from the offset a 409 upload_offset names", async () => {
    const server = fakeServer({ chunkSize: 2 * MB });
    // Another tab (or an earlier try) already sent the first chunk of this session.
    await uploadFile(fileOf(1, "x"), { retryDelay: noDelay }); // up1
    const file = fileOf(5 * MB);
    let once = 1;
    server.faults.push((req) => {
      if (req.method === "PUT" && req.path === "/uploads/up2" && req.offset === 0 && once-- > 0) {
        const upload = server.sessions.get("up2");
        if (upload) upload.received = 2 * MB;
        return json(409, {
          error: {
            code: "upload_offset",
            message: "o",
            details: { received: String(2 * MB), complete: "false" },
          },
        });
      }
      return undefined;
    });
    await uploadFile(file, { retryDelay: noDelay });
    expect(
      server
        .puts()
        .filter((p) => p.path === "/uploads/up2")
        .map((p) => p.offset),
    ).toEqual([0, 2 * MB, 4 * MB]);
  });

  it("resumes an upload it started before, from what that one received", async () => {
    const server = fakeServer({ chunkSize: 2 * MB });
    const file = fileOf(5 * MB);
    const controller = new AbortController();
    let sessionId = "";
    await uploadFile(file, {
      signal: controller.signal,
      onSession: (u) => {
        sessionId = u.id;
      },
      onProgress: (p) => {
        if (p.sent >= 2 * MB) controller.abort();
      },
      retryDelay: noDelay,
    }).catch(() => undefined);
    expect(sessionId).toBe("up1");
    const before = server.puts().length;
    const done = await uploadFile(file, { resume: sessionId, retryDelay: noDelay });
    expect(done.id).toBe("up1");
    expect(server.seen.filter((s) => s.method === "POST")).toHaveLength(1);
    expect(
      server
        .puts()
        .slice(before)
        .map((p) => p.offset),
    ).toEqual([2 * MB, 4 * MB]);
  });

  it("starts a new session when the one to resume is gone", async () => {
    const server = fakeServer();
    const done = await uploadFile(fileOf(10), { resume: "expired", retryDelay: noDelay });
    expect(done.id).toBe("up1");
    expect(server.seen.map((s) => `${s.method} ${s.path}`)).toEqual([
      "GET /uploads/expired",
      "POST /uploads",
      "PUT /uploads/up1",
    ]);
  });

  it("stops at once on abort: no request after the signal", async () => {
    const server = fakeServer({ chunkSize: 2 * MB });
    const controller = new AbortController();
    const result = uploadFile(fileOf(9 * MB), {
      signal: controller.signal,
      onProgress: (p) => {
        if (p.sent === 2 * MB) controller.abort();
      },
      retryDelay: noDelay,
    });
    await expect(result).rejects.toMatchObject({ name: "AbortError" });
    // The first chunk went through, and the abort came with its answer: nothing after it.
    expect(server.puts().map((p) => p.offset)).toEqual([0]);
    const count = server.seen.length;
    await new Promise((r) => setTimeout(r, 10));
    expect(server.seen).toHaveLength(count);
  });

  it("stops during the pause before a retry when aborted", async () => {
    const server = fakeServer({ chunkSize: 2 * MB });
    server.faults.push((req) => (req.method === "PUT" ? "network" : undefined));
    const controller = new AbortController();
    const result = uploadFile(fileOf(3 * MB), {
      signal: controller.signal,
      retryDelay: () => 60_000,
    });
    await vi.waitFor(() => expect(server.puts()).toHaveLength(1));
    controller.abort();
    await expect(result).rejects.toMatchObject({ name: "AbortError" });
    expect(server.puts()).toHaveLength(1);
  });

  it("does not start when the signal is already aborted", async () => {
    const server = fakeServer();
    const controller = new AbortController();
    controller.abort();
    await expect(uploadFile(fileOf(10), { signal: controller.signal })).rejects.toMatchObject({
      name: "AbortError",
    });
    expect(server.seen).toHaveLength(0);
  });

  it.each([
    [413, "file_too_large", { max_size: String(5 * 1024 * MB) }],
    [507, "insufficient_storage", {}],
    [429, "rate_limited", {}],
  ])(
    "maps a refused session (%i %s) to an UploadError, without retrying",
    async (status, code, details) => {
      const server = fakeServer();
      server.faults.push((req) =>
        req.method === "POST"
          ? json(status, { error: { code, message: code, details } })
          : undefined,
      );
      const err = await uploadFile(fileOf(10, "big.iso"), { retryDelay: noDelay }).catch((e) => e);
      expect(isUploadError(err)).toBe(true);
      expect(err).toMatchObject({ status, code, details, fileName: "big.iso" });
      expect(server.seen).toHaveLength(1);
    },
  );

  it("does not retry a chunk refused for good (507 while writing)", async () => {
    const server = fakeServer({ chunkSize: 2 * MB });
    server.faults.push((req) =>
      req.method === "PUT" ? apiError(507, "insufficient_storage") : undefined,
    );
    const err = await uploadFile(fileOf(3 * MB), { retryDelay: noDelay }).catch((e) => e);
    expect(err).toMatchObject({ status: 507, code: "insufficient_storage" });
    expect(server.puts()).toHaveLength(1);
  });

  it("learns the server's limits from the session's answer", async () => {
    fakeServer({ chunkSize: MB, maxSize: 3 * MB });
    expect(knownMaxFileSize()).toBeUndefined();
    expect(fitsOneRequest([fileOf(2 * MB)])).toBe(true);
    await uploadFile(fileOf(10), { retryDelay: noDelay });
    expect(knownMaxFileSize()).toBe(3 * MB);
    // A chunk size under 50 MB is the server's single-request size: no multipart over it.
    expect(fitsOneRequest([fileOf(2 * MB)])).toBe(false);
    expect(fitsOneRequest([fileOf(MB / 2), fileOf(MB / 2)])).toBe(true);
  });

  it("sends files in one request only while they weigh DIRECT_POST_BYTES together", () => {
    expect(fitsOneRequest([fileOf(DIRECT_POST_BYTES)])).toBe(true);
    expect(fitsOneRequest([fileOf(DIRECT_POST_BYTES / 2), fileOf(DIRECT_POST_BYTES / 2 + 1)])).toBe(
      false,
    );
  });
});
