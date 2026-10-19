import { InfiniteQueryObserver, QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { type MessagePages, queryKeys, roomMessagesQuery } from "@/lib/api/queries";
import { FakeSocket } from "@/test/fake-socket";
import { makeMessage } from "@/test/fixtures";
import { startLive } from "./index";
import { backoffDelay, createLiveSocket, liveUrl } from "./socket";
import { useLiveStore } from "./store";

describe("live socket (UI-06)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.instances = [];
  });
  afterEach(() => {
    vi.useRealTimers();
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
