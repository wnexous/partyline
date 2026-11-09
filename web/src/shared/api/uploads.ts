import { ApiError, api, isApiError } from "./client";
import {
  ErrorCodeNotFound,
  ErrorCodeUploadBusy,
  ErrorCodeUploadOffset,
  type Upload,
} from "./types.gen";

/*
 * Upload sessions (LF-01, docs/api.md "Files and upload sessions"): a file larger than one request
 * goes up in chunks and the message then attaches it by id. The client creates the session, sends
 * `file.slice(offset, offset + chunk)` with PUT ?offset= one chunk at a time, and the chunk that
 * brings the last byte completes it. A chunk is kept whole or not at all, so a failed one (no
 * network, a 5xx, a chunk cut short) is sent again from what the server says it received, up to
 * three times with a growing pause; a 409 upload_offset says where to go on. An aborted signal
 * stops it before the next request.
 *
 * The server tells its limits only in the session's answer (there is no config endpoint):
 * `chunk_size` is 50 MB or the single-request limit when that is lower, `max_size` the largest file.
 * The client keeps the last ones it saw, so it knows from then on how much one request may carry.
 */

/** How far one file got: bytes the server holds of `total`. */
export interface UploadProgress {
  sent: number;
  total: number;
}

export interface UploadOptions {
  signal?: AbortSignal;
  onProgress?: (progress: UploadProgress) => void;
  /** An upload this file already started (an earlier send that failed or was cancelled): it goes on from there. */
  resume?: string;
  /** The session the file goes up in, once it exists; keep its id to resume it later. */
  onSession?: (upload: Upload) => void;
  /** Tries per chunk after the first; 3 by default. */
  retries?: number;
  /** The pause before retry `attempt` (1, 2, 3…), in milliseconds. */
  retryDelay?: (attempt: number) => number;
}

/**
 * The largest chunk the web app sends: smaller than the server's 50 MB, so progress moves every few
 * seconds on a phone and a retry resends little. The server's own `chunk_size` caps it further.
 */
export const CLIENT_CHUNK_BYTES = 8 << 20;

/**
 * What the files of one message may weigh together to go as one multipart request, the way small
 * files always went (one request, no session). Above it every file of the message goes through a
 * session, so the order of the attachments stays the order they were added in.
 */
export const DIRECT_POST_BYTES = 8 << 20;

const DEFAULT_RETRIES = 3;
const defaultDelay = (attempt: number) => 1_000 * 2 ** (attempt - 1);

/** The server's answers about its limits, from the last session seen in this tab. */
const learned: { chunkSize?: number; maxSize?: number } = {};

function learn(upload: Upload) {
  if (upload.chunk_size > 0) learned.chunkSize = upload.chunk_size;
  if (upload.max_size > 0) learned.maxSize = upload.max_size;
}

/** The largest file this server takes through sessions, once a session said it. */
export const knownMaxFileSize = (): number | undefined => learned.maxSize;

/** Forgets what the sessions said; for tests. */
export function resetUploadLimits() {
  learned.chunkSize = undefined;
  learned.maxSize = undefined;
}

/**
 * Whether these files go in one multipart post: together at most DIRECT_POST_BYTES and at most the
 * server's single-request size when a session told it (`chunk_size` below 50 MB is exactly that).
 */
export function fitsOneRequest(files: readonly Blob[]): boolean {
  const total = files.reduce((sum, f) => sum + f.size, 0);
  return total <= Math.min(DIRECT_POST_BYTES, learned.chunkSize ?? DIRECT_POST_BYTES);
}

/** An upload that failed for good: the API error and the name of the file it was for. */
export class UploadError extends ApiError {
  readonly fileName: string;

  constructor(err: ApiError, fileName: string) {
    super(err.status, err.code, err.message, err.fields, err.refused, err.details);
    this.name = "UploadError";
    this.fileName = fileName;
  }
}

export function isUploadError(err: unknown): err is UploadError {
  return err instanceof UploadError;
}

/** Whether err is the end of an aborted request (the person cancelled). */
export function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === "AbortError";
}

function abortError(): DOMException {
  return new DOMException("The upload was cancelled.", "AbortError");
}

function throwIfAborted(signal?: AbortSignal) {
  if (signal?.aborted) throw abortError();
}

/** Waits ms, or rejects with AbortError as soon as the signal aborts. */
function pause(ms: number, signal?: AbortSignal): Promise<void> {
  throwIfAborted(signal);
  if (ms <= 0) return Promise.resolve();
  return new Promise((done, fail) => {
    const onAbort = () => {
      clearTimeout(timer);
      fail(abortError());
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      done();
    }, ms);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

/**
 * A failed chunk that may go through when sent again: no network, the server busy or failing, a
 * chunk that did not arrive whole (400). A full disk (507) or a refused size are final.
 */
function retryable(err: unknown): boolean {
  if (!isApiError(err)) return false;
  if (err.status === 0 || err.status === 400 || err.status === 408 || err.status === 429) {
    return true;
  }
  if (err.status === 409) return err.code === ErrorCodeUploadBusy;
  return err.status >= 500 && err.status !== 507;
}

const path = (id: string) => `/uploads/${encodeURIComponent(id)}`;

/** The session to go on with: the one named by `resume` when it is still this file's, else a new one. */
async function openSession(file: File, opts: UploadOptions): Promise<Upload> {
  if (opts.resume) {
    try {
      const found = await api<Upload>("GET", path(opts.resume), undefined, { signal: opts.signal });
      if (found.size === file.size && found.name === file.name) return found;
    } catch (err) {
      // A session that expired or was used up is gone: start again. Anything else is the network
      // or the server, which a new session would meet too.
      if (!(isApiError(err) && err.code === ErrorCodeNotFound)) throw err;
    }
  }
  return api<Upload>(
    "POST",
    "/uploads",
    { name: file.name, mime: file.type || "application/octet-stream", size: file.size },
    { signal: opts.signal },
  );
}

/**
 * Sends one file through an upload session and resolves with the finished upload (its id goes in
 * the message's `upload_ids`). Throws UploadError when the server refuses it or a chunk keeps
 * failing, and the AbortError of the signal when it is cancelled.
 */
export async function uploadFile(file: File, opts: UploadOptions = {}): Promise<Upload> {
  const { signal, onProgress } = opts;
  const retries = opts.retries ?? DEFAULT_RETRIES;
  const delay = opts.retryDelay ?? defaultDelay;
  const report = (sent: number) => onProgress?.({ sent, total: file.size });
  try {
    throwIfAborted(signal);
    let upload = await openSession(file, opts);
    learn(upload);
    opts.onSession?.(upload);
    report(upload.received);
    // Failures since the last chunk that went through.
    let failures = 0;
    while (!upload.complete) {
      throwIfAborted(signal);
      const offset = upload.received;
      const chunk = Math.max(
        1,
        Math.min(CLIENT_CHUNK_BYTES, upload.chunk_size || CLIENT_CHUNK_BYTES),
      );
      const end = Math.min(offset + chunk, file.size);
      try {
        upload = await api<Upload>("PUT", path(upload.id), file.slice(offset, end), {
          query: { offset },
          signal,
        });
        failures = 0;
        report(upload.received);
      } catch (err) {
        if (isAbortError(err)) throw err;
        if (failures >= retries) throw err;
        failures += 1;
        if (isApiError(err) && err.code === ErrorCodeUploadOffset) {
          // The server holds a different amount than this client thought (an answer that got
          // lost): go on from there, or ask for the finished upload when it needs no more.
          const received = Number(err.details.received);
          if (err.details.complete === "true" || !Number.isFinite(received)) {
            upload = await api<Upload>("GET", path(upload.id), undefined, { signal });
          } else {
            upload = { ...upload, received };
          }
          report(upload.received);
          continue;
        }
        if (!retryable(err)) throw err;
        await pause(delay(failures), signal);
        // Where the server is now: the failed chunk is there whole or not at all.
        try {
          upload = await api<Upload>("GET", path(upload.id), undefined, { signal });
          report(upload.received);
        } catch (asked) {
          if (isAbortError(asked) || !retryable(asked)) throw asked;
          // Still unreachable: the next try of the chunk finds out.
        }
      }
    }
    return upload;
  } catch (err) {
    if (isApiError(err) && !(err instanceof UploadError)) throw new UploadError(err, file.name);
    throw err;
  }
}
