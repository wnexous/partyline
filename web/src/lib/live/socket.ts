import type { LiveEvent } from "@/shared/api/types.gen";
import type { SocketStatus } from "./store";

/*
 * The live WebSocket (GET /api/v1/live, cookie auth). Reconnects forever with exponential
 * backoff from 1 s to 30 s; the delay resets once a connection opens. The one exception is
 * close code 1008 (`session revoked`): the session is over, so reconnecting would only fail.
 * 1013 (`session check failed`, the server could not re-check the session) is a normal retry.
 *
 * A hidden tab is throttled and its dials fail, so the backoff it builds up can be 30 s when the
 * user comes back. `wake()` dials at once instead (on `visibilitychange` to visible and on
 * `online`), so returning to the app never waits out a delay earned while it was away.
 */

/** Close code the server sends when the socket's session or token was revoked. */
export const CLOSE_SESSION_REVOKED = 1008;

export const MIN_BACKOFF_MS = 1_000;
export const MAX_BACKOFF_MS = 30_000;

export function backoffDelay(attempt: number): number {
  return Math.min(MAX_BACKOFF_MS, MIN_BACKOFF_MS * 2 ** Math.max(0, attempt));
}

export function liveUrl(loc: Pick<Location, "protocol" | "host"> = window.location): string {
  return `${loc.protocol === "https:" ? "wss:" : "ws:"}//${loc.host}/api/v1/live`;
}

export interface LiveSocketOptions {
  url?: string;
  onEvent: (event: LiveEvent) => void;
  onStatus?: (status: SocketStatus) => void;
  /** Called when a connection opens after an earlier one was lost (not on the first open). */
  onReconnect?: () => void;
  /** Called once when the server ends the session (close 1008); the socket stays closed. */
  onSessionEnded?: () => void;
  /** Called when the page becomes visible again, before the socket wakes up. */
  onVisible?: () => void;
  createSocket?: (url: string) => WebSocket;
}

export interface LiveSocket {
  start: () => void;
  stop: () => void;
  /** Dials now instead of waiting out the backoff, unless stopped or already connected. */
  wake: () => void;
}

function isLiveEvent(value: unknown): value is LiveEvent {
  return (
    typeof value === "object" &&
    value !== null &&
    typeof (value as { type?: unknown }).type === "string"
  );
}

export function createLiveSocket(options: LiveSocketOptions): LiveSocket {
  const create = options.createSocket ?? ((url: string) => new WebSocket(url));
  let socket: WebSocket | null = null;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let attempt = 0;
  let everOpened = false;
  let stopped = true;

  const setStatus = (s: SocketStatus) => options.onStatus?.(s);

  const connect = () => {
    timer = null;
    if (stopped) return;
    setStatus("connecting");
    const ws = create(options.url ?? liveUrl());
    socket = ws;

    ws.onopen = () => {
      if (socket !== ws) return;
      attempt = 0;
      setStatus("open");
      if (everOpened) options.onReconnect?.();
      everOpened = true;
    };

    ws.onmessage = (msg: MessageEvent) => {
      if (socket !== ws || typeof msg.data !== "string") return;
      let parsed: unknown;
      try {
        parsed = JSON.parse(msg.data);
      } catch {
        return;
      }
      if (isLiveEvent(parsed)) options.onEvent(parsed);
    };

    ws.onclose = (event: CloseEvent) => {
      if (socket !== ws) return;
      socket = null;
      setStatus("closed");
      if (stopped) return;
      if (event.code === CLOSE_SESSION_REVOKED) {
        stopped = true;
        options.onSessionEnded?.();
        return;
      }
      timer = setTimeout(connect, backoffDelay(attempt));
      attempt += 1;
    };
  };

  const wake = () => {
    // A socket is open or dialing: there is nothing to wake. A stopped one (close 1008 included)
    // must stay down.
    if (stopped || socket !== null) return;
    if (timer) clearTimeout(timer);
    timer = null;
    attempt = 0;
    connect();
  };

  const onVisibilityChange = () => {
    if (typeof document === "undefined" || document.visibilityState !== "visible") return;
    options.onVisible?.();
    wake();
  };

  const onOnline = () => wake();

  return {
    start() {
      if (!stopped) return;
      stopped = false;
      if (typeof document !== "undefined") {
        document.addEventListener("visibilitychange", onVisibilityChange);
      }
      if (typeof window !== "undefined") window.addEventListener("online", onOnline);
      connect();
    },
    stop() {
      stopped = true;
      if (typeof document !== "undefined") {
        document.removeEventListener("visibilitychange", onVisibilityChange);
      }
      if (typeof window !== "undefined") window.removeEventListener("online", onOnline);
      if (timer) clearTimeout(timer);
      timer = null;
      const ws = socket;
      socket = null;
      ws?.close();
      setStatus("closed");
    },
    wake,
  };
}
