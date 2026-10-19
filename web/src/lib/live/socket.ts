import type { LiveEvent } from "@/lib/api/types.gen";
import type { SocketStatus } from "./store";

/*
 * The live WebSocket (GET /api/v1/live, cookie auth). Reconnects forever with exponential
 * backoff from 1 s to 30 s; the delay resets once a connection opens.
 */

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
  createSocket?: (url: string) => WebSocket;
}

export interface LiveSocket {
  start: () => void;
  stop: () => void;
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

    ws.onclose = () => {
      if (socket !== ws) return;
      socket = null;
      setStatus("closed");
      if (stopped) return;
      timer = setTimeout(connect, backoffDelay(attempt));
      attempt += 1;
    };
  };

  return {
    start() {
      if (!stopped) return;
      stopped = false;
      connect();
    },
    stop() {
      stopped = true;
      if (timer) clearTimeout(timer);
      timer = null;
      const ws = socket;
      socket = null;
      ws?.close();
      setStatus("closed");
    },
  };
}
