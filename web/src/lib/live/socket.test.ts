import { InfiniteQueryObserver, QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setUnauthorizedHandler } from "@/shared/api/client";
import { type MessagePages, queryKeys, roomMessagesQuery } from "@/lib/api/queries";
import { FakeSocket } from "@/test/fake-socket";
import { makeMe, makeMessage, makeRoom } from "@/test/fixtures";
import { startLive } from "./index";
import { backoffDelay, createLiveSocket, liveUrl } from "./socket";
import { useLiveStore } from "./store";

/** Makes the document report `state` and fires the event the browser fires when it changes. */
function setTabState(state: "visible" | "hidden") {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
  document.dispatchEvent(new Event("visibilitychange"));
}

function dropTabStateShadow() {
  // Drops the shadowing property so the prototype's getter answers again.
  delete (document as unknown as Record<string, unknown>).visibilityState;
}

describe("live socket (UI-06)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.instances = [];
  });
  afterEach(() => {
    vi.useRealTimers();
    dropTabStateShadow();
  });

  it("backs off from 1 s doubling up to 30 s", () => {
    expect([0, 1, 2, 3, 4, 5, 6, 10].map(backoffDelay)).toEqual([
      1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000,
    ]);
  });

  it("uses wss on https and ws on http", () => {
    expect(liveUrl({ protocol: "https:", host: "pl.example" })).toBe(
      "wss://pl.example/api/v1/live",
    );
    expect(liveUrl({ protocol: "http:", host: "localhost:5173" })).toBe(
      "ws://localhost:5173/api/v1/live",
    );
  });

  it("reports status and reconnects after the backoff delay", () => {
    const statuses: string[] = [];
    const onReconnect = vi.fn();
    const live = createLiveSocket({
      url: "ws://test/api/v1/live",
      createSocket: FakeSocket.create,
      onEvent: () => {},
      onStatus: (s) => statuses.push(s),
      onReconnect,
    });
    live.start();
    FakeSocket.latest().open();
    expect(statuses).toEqual(["connecting", "open"]);
    expect(onReconnect).not.toHaveBeenCalled();

    FakeSocket.latest().drop();
    expect(statuses.at(-1)).toBe("closed");
    vi.advanceTimersByTime(999);
    expect(FakeSocket.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.instances).toHaveLength(2);

    // A failed retry doubles the delay.
    FakeSocket.latest().drop();
    vi.advanceTimersByTime(1999);
    expect(FakeSocket.instances).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.instances).toHaveLength(3);

    FakeSocket.latest().open();
    expect(onReconnect).toHaveBeenCalledTimes(1);
    expect(statuses.at(-1)).toBe("open");
    live.stop();
  });

  it("resets the delay to 1 s once a connection opens", () => {
    const live = createLiveSocket({
      url: "ws://test/api/v1/live",
      createSocket: FakeSocket.create,
      onEvent: () => {},
    });
    live.start();
    // Three failed attempts: 1 s, 2 s, then 4 s would be next.
    FakeSocket.latest().drop();
    vi.advanceTimersByTime(1_000);
    FakeSocket.latest().drop();
    vi.advanceTimersByTime(2_000);
    FakeSocket.latest().drop();
    vi.advanceTimersByTime(4_000);
    expect(FakeSocket.instances).toHaveLength(4);

    // This one connects; the next drop retries after 1 s again.
    FakeSocket.latest().open();
    FakeSocket.latest().drop();
    vi.advanceTimersByTime(999);
    expect(FakeSocket.instances).toHaveLength(4);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.instances).toHaveLength(5);
    live.stop();
  });

  it("parses frames into events and ignores malformed ones", () => {
    const onEvent = vi.fn();
    const live = createLiveSocket({ url: "ws://t", createSocket: FakeSocket.create, onEvent });
    live.start();
    FakeSocket.latest().open();
    FakeSocket.latest().emit("not json");
    FakeSocket.latest().emit({ nope: 1 });
    FakeSocket.latest().emit({ type: "lagged" });
    expect(onEvent).toHaveBeenCalledTimes(1);
    expect(onEvent).toHaveBeenCalledWith({ type: "lagged" });
    live.stop();
  });

  it("treats close 1008 (session revoked) as logged out and does not reconnect", () => {
    const statuses: string[] = [];
    const onSessionEnded = vi.fn();
    const live = createLiveSocket({
      url: "ws://t",
      createSocket: FakeSocket.create,
      onEvent: () => {},
      onStatus: (s) => statuses.push(s),
      onSessionEnded,
    });
    live.start();
    FakeSocket.latest().open();
    FakeSocket.latest().drop(1008, "session revoked");
    expect(onSessionEnded).toHaveBeenCalledTimes(1);
    expect(statuses.at(-1)).toBe("closed");
    vi.advanceTimersByTime(5 * 60_000);
    expect(FakeSocket.instances).toHaveLength(1);
    live.stop();
  });

  it("reconnects with the normal backoff after close 1013 (session check failed)", () => {
    const onSessionEnded = vi.fn();
    const live = createLiveSocket({
      url: "ws://t",
      createSocket: FakeSocket.create,
      onEvent: () => {},
      onSessionEnded,
    });
    live.start();
    FakeSocket.latest().open();
    FakeSocket.latest().drop(1013, "session check failed");
    vi.advanceTimersByTime(999);
    expect(FakeSocket.instances).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.instances).toHaveLength(2);
    // A second failure doubles the delay, as for any other drop.
    FakeSocket.latest().drop(1013, "session check failed");
    vi.advanceTimersByTime(1999);
    expect(FakeSocket.instances).toHaveLength(2);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.instances).toHaveLength(3);
    expect(onSessionEnded).not.toHaveBeenCalled();
    live.stop();
  });

  it("does not reconnect after stop", () => {
    const live = createLiveSocket({
      url: "ws://t",
      createSocket: FakeSocket.create,
      onEvent: () => {},
    });
    live.start();
    FakeSocket.latest().open();
    live.stop();
    vi.advanceTimersByTime(60_000);
    expect(FakeSocket.instances).toHaveLength(1);
    expect(FakeSocket.latest().closed).toBe(true);
  });

  it("dials at once when the tab comes back, then backs off from 1 s again", () => {
    const onVisible = vi.fn();
    const live = createLiveSocket({
      url: "ws://t",
      createSocket: FakeSocket.create,
      onEvent: () => {},
      onVisible,
    });
    live.start();
    FakeSocket.latest().open();

    // Backgrounded, the browser closes the socket and the retries keep failing: each drop doubles
    // the wait, until the next dial is 30 s away.
    setTabState("hidden");
    FakeSocket.latest().drop();
    for (const wait of [1_000, 2_000, 4_000, 8_000, 16_000]) {
      vi.advanceTimersByTime(wait);
      FakeSocket.latest().drop();
    }
    expect(FakeSocket.instances).toHaveLength(6);
    // A tab that is still hidden wakes nothing.
    setTabState("hidden");
    vi.advanceTimersByTime(29_000);
    expect(FakeSocket.instances).toHaveLength(6);

    // Back on screen: the stale 30 s wait is dropped and a socket is dialed now.
    setTabState("visible");
    expect(onVisible).toHaveBeenCalledTimes(1);
    expect(FakeSocket.instances).toHaveLength(7);

    // The dial fails too, and the next wait is 1 s again, not where it left off.
    FakeSocket.latest().drop();
    vi.advanceTimersByTime(999);
    expect(FakeSocket.instances).toHaveLength(7);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.instances).toHaveLength(8);
    live.stop();
  });

  it("dials at once when the browser comes back online", () => {
    const live = createLiveSocket({
      url: "ws://t",
      createSocket: FakeSocket.create,
      onEvent: () => {},
    });
    live.start();
    FakeSocket.latest().open();
    FakeSocket.latest().drop();

    window.dispatchEvent(new Event("online"));
    expect(FakeSocket.instances).toHaveLength(2);
    // The pending retry was cancelled: no second socket when its delay elapses.
    vi.advanceTimersByTime(1_000);
    expect(FakeSocket.instances).toHaveLength(2);
    live.stop();
  });

  it("opens no second socket when woken while connecting, open or stopped", () => {
    const live = createLiveSocket({
      url: "ws://t",
      createSocket: FakeSocket.create,
      onEvent: () => {},
    });
    live.start();
    live.wake();
    expect(FakeSocket.instances).toHaveLength(1);
    FakeSocket.latest().open();
    live.wake();
    expect(FakeSocket.instances).toHaveLength(1);
    live.stop();
    live.wake();
    expect(FakeSocket.instances).toHaveLength(1);
  });

  it("stops listening for wake-ups after stop", () => {
    const live = createLiveSocket({
      url: "ws://t",
      createSocket: FakeSocket.create,
      onEvent: () => {},
    });
    live.start();
    FakeSocket.latest().open();
    live.stop();
    setTabState("visible");
    window.dispatchEvent(new Event("online"));
    vi.advanceTimersByTime(60_000);
    expect(FakeSocket.instances).toHaveLength(1);
  });

  it("does not wake a socket the server ended (close 1008)", () => {
    const live = createLiveSocket({
      url: "ws://t",
      createSocket: FakeSocket.create,
      onEvent: () => {},
      onSessionEnded: () => {},
    });
    live.start();
    FakeSocket.latest().open();
    FakeSocket.latest().drop(1008, "session revoked");
    setTabState("visible");
    window.dispatchEvent(new Event("online"));
    live.wake();
    expect(FakeSocket.instances).toHaveLength(1);
    live.stop();
  });
});

describe("startLive", () => {
  const fetchMock = vi.fn<typeof fetch>();

  beforeEach(() => {
    FakeSocket.instances = [];
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => {
    fetchMock.mockReset();
    vi.unstubAllGlobals();
  });

  it("patches the cache from events and mirrors the socket status in the store", () => {
    const qc = new QueryClient();
    qc.setQueryData<MessagePages>(queryKeys.roomMessages("r1"), {
      pages: [{ items: [makeMessage({ seq: 1 })], has_more: false }],
      pageParams: [undefined],
    });
    const stop = startLive(qc, { url: "ws://t", createSocket: FakeSocket.create });
    expect(useLiveStore.getState().status).toBe("connecting");
    FakeSocket.latest().open();
    expect(useLiveStore.getState().status).toBe("open");
    FakeSocket.latest().emit({ type: "room.message", data: makeMessage({ seq: 2 }) });
    const seqs = qc
      .getQueryData<MessagePages>(queryKeys.roomMessages("r1"))
      ?.pages[0]?.items.map((m) => m.seq);
    expect(seqs).toEqual([1, 2]);
    stop();
    expect(useLiveStore.getState().status).toBe("closed");
  });

  it("on close 1008 clears the cache and runs the 401 handler with the current location", () => {
    vi.useFakeTimers();
    const qc = new QueryClient();
    qc.setQueryData(queryKeys.rooms, [makeRoom()]);
    qc.setQueryData(queryKeys.me, makeMe());
    const onUnauthorized = vi.fn();
    const unregister = setUnauthorizedHandler(onUnauthorized);
    window.history.replaceState(null, "", "/agents?tab=mine");

    const stop = startLive(qc, { url: "ws://t", createSocket: FakeSocket.create });
    FakeSocket.latest().open();
    FakeSocket.latest().drop(1008, "session revoked");

    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(onUnauthorized).toHaveBeenCalledWith("/agents?tab=mine");
    expect(qc.getQueryCache().getAll()).toEqual([]);
    vi.advanceTimersByTime(5 * 60_000);
    expect(FakeSocket.instances).toHaveLength(1);
    stop();
    unregister();
    window.history.replaceState(null, "", "/");
    vi.useRealTimers();
  });

  it("on reconnect fetches the open room after its last seen sequence and merges it", async () => {
    vi.useFakeTimers();
    const qc = new QueryClient();
    qc.setQueryData<MessagePages>(queryKeys.roomMessages("r1"), {
      pages: [{ items: [makeMessage({ seq: 59 }), makeMessage({ seq: 60 })], has_more: true }],
      pageParams: [undefined],
    });
    // An observer makes the timeline "open" (active), like a mounted room page.
    const observer = new InfiniteQueryObserver(qc, roomMessagesQuery("r1"));
    const unsubscribe = observer.subscribe(() => {});
    fetchMock.mockImplementation(async (url) => {
      if (String(url).startsWith("/api/v1/rooms/r1/messages")) {
        return new Response(
          JSON.stringify({
            items: [makeMessage({ seq: 61 }), makeMessage({ seq: 62 })],
            has_more: false,
          }),
        );
      }
      return new Response(JSON.stringify({ items: [] }));
    });

    const stop = startLive(qc, { url: "ws://t", createSocket: FakeSocket.create });
    FakeSocket.latest().open();
    FakeSocket.latest().drop();
    vi.advanceTimersByTime(1000);
    FakeSocket.latest().open();
    vi.useRealTimers();
    await vi.waitFor(() =>
      expect(
        qc
          .getQueryData<MessagePages>(queryKeys.roomMessages("r1"))
          ?.pages[0]?.items.map((m) => m.seq),
      ).toEqual([59, 60, 61, 62]),
    );
    const urls = fetchMock.mock.calls.map((c) => String(c[0]));
    expect(urls).toContain("/api/v1/rooms/r1/messages?after_seq=60&limit=50");
    unsubscribe();
    stop();
  });
});
