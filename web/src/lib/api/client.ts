import type { ErrorBody, ErrorCode, RefusedMember } from "./types.gen";

/** Every API path is relative to this prefix (see internal/api/dto). */
export const API_BASE = "/api/v1";

/**
 * An API failure. `fields` maps request field names to validation reasons (422); `refused` lists
 * the members a new group could not take (422 members_refused), in the order they were asked for;
 * `details` carries the facts of a refusal that are not request fields (the counts of
 * organization_not_empty).
 */
export class ApiError extends Error {
  readonly status: number;
  readonly code: ErrorCode;
  readonly fields: Record<string, string>;
  readonly refused: RefusedMember[];
  readonly details: Record<string, string>;

  constructor(
    status: number,
    code: ErrorCode,
    message: string,
    fields: Record<string, string> = {},
    refused: RefusedMember[] = [],
    details: Record<string, string> = {},
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.fields = fields;
    this.refused = refused;
    this.details = details;
  }
}

export function isApiError(err: unknown): err is ApiError {
  return err instanceof ApiError;
}

type UnauthorizedHandler = (returnTo: string) => void | Promise<void>;

let onUnauthorized: UnauthorizedHandler | null = null;

/**
 * Registers what happens when a request answers 401 (UI-02): the handler receives the current
 * in-app location so login can return there. Returns a function that unregisters it.
 */
export function setUnauthorizedHandler(handler: UnauthorizedHandler | null): () => void {
  onUnauthorized = handler;
  return () => {
    if (onUnauthorized === handler) onUnauthorized = null;
  };
}

/**
 * Runs the 401 handler for the current location: the session ended (UI-02). Returns what the
 * handler returned, so a caller that has to wait for the redirect (the live socket does) can.
 */
export function notifyUnauthorized(): void | Promise<void> {
  return onUnauthorized?.(currentLocation());
}

export function currentLocation(): string {
  const { pathname, search, hash } = window.location;
  return `${pathname}${search}${hash}`;
}

export type HttpMethod = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

export interface RequestOptions {
  /** Query string parameters; undefined values are dropped. */
  query?: Record<string, string | number | boolean | undefined>;
  /** Skip the global 401 handler (login and setup answer 401 for bad credentials). */
  skipAuthRedirect?: boolean;
  signal?: AbortSignal;
}

const STATUS_CODES: Record<number, ErrorCode> = {
  400: "invalid",
  401: "unauthorized",
  403: "forbidden",
  404: "not_found",
  409: "conflict",
  410: "gone",
  413: "too_large",
  422: "invalid",
  429: "rate_limited",
  503: "unavailable",
};

function buildUrl(path: string, query?: RequestOptions["query"]): string {
  const url = `${API_BASE}${path.startsWith("/") ? path : `/${path}`}`;
  if (!query) return url;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined) params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `${url}?${qs}` : url;
}

async function toApiError(res: Response): Promise<ApiError> {
  const fallback = STATUS_CODES[res.status] ?? "internal";
  let body: Partial<ErrorBody> | undefined;
  try {
    body = (await res.json()) as Partial<ErrorBody>;
  } catch {
    body = undefined;
  }
  const e = body?.error;
  return new ApiError(
    res.status,
    e?.code ?? fallback,
    e?.message || res.statusText || fallback,
    e?.fields ?? {},
    e?.refused ?? [],
    e?.details ?? {},
  );
}

/**
 * Calls the partyline API. JSON bodies are serialized; FormData is sent as multipart; a Blob (a
 * File) goes as it is with its own content type (avatar upload). Resolves with the decoded JSON
 * (undefined for empty responses) or throws ApiError.
 */
export async function api<T>(
  method: HttpMethod,
  path: string,
  body?: unknown,
  options: RequestOptions = {},
): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  let payload: BodyInit | undefined;
  if (body instanceof FormData) {
    payload = body;
  } else if (body instanceof Blob) {
    payload = body;
    headers["Content-Type"] = body.type || "application/octet-stream";
  } else if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    payload = JSON.stringify(body);
  }

  let res: Response;
  try {
    res = await fetch(buildUrl(path, options.query), {
      method,
      headers,
      body: payload,
      credentials: "same-origin",
      signal: options.signal,
    });
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") throw err;
    throw new ApiError(0, "unavailable", "network error");
  }

  if (!res.ok) {
    const error = await toApiError(res);
    if (res.status === 401 && !options.skipAuthRedirect) notifyUnauthorized();
    throw error;
  }

  if (res.status === 204) return undefined as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}
