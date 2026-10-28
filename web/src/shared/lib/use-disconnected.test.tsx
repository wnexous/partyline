import { QueryClient } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { startLive } from "@/lib/live";
import { useLiveStore } from "@/lib/live/store";
import { FakeSocket } from "@/test/fake-socket";
import { useDisconnected } from "./use-disconnected";

/*
 * The banner before the UI admits the live line is down (UI-06). A tab that comes back gets a
 * fresh grace: the drop it saw while hidden is minutes old, and showing the banner at once on
 * every return is the bug this covers.
 */

/** Makes the document report `state` and fires the event the browser fires when it changes. */
function setTabState(state: "visible" | "hidden") {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
  act(() => {
    document.dispatchEvent(new Event("visibilitychange"));
  });
}

describe("useDisconnected (UI-06)", () => {
  let stop: () => void;

  beforeEach(() => {
    vi.useFakeTimers();
    FakeSocket.instances = [];
    useLiveStore.setState({ status: "closed", offlineSince: null });
    stop = startLive(new QueryClient(), { url: "ws://t", createSocket: FakeSocket.create });
    act(() => FakeSocket.latest().open());
  });
  afterEach(() => {
    stop();
    vi.useRealTimers();
    useLiveStore.setState({ status: "closed", offlineSince: null });
    // Drops the shadowing property so the prototype's getter answers again.
    delete (document as unknown as Record<string, unknown>).visibilityState;
  });

  it("shows no banner for a line that is open", () => {
    const { result } = renderHook(() => useDisconnected());
    expect(result.current).toBe(false);
  });

  it("gives a returning tab a fresh grace and drops the banner as soon as the line opens", () => {
    const { result } = renderHook(() => useDisconnected());

    // Backgrounded: the browser closes the socket and the retries fail; the banner's grace runs
    // out while nobody is looking.
    setTabState("hidden");
    act(() => FakeSocket.latest().drop());
    act(() => vi.advanceTimersByTime(1_000));
    act(() => FakeSocket.latest().drop());
    act(() => vi.advanceTimersByTime(1_000));
    expect(result.current).toBe(true);

    // Minutes later the timestamp the grace counts from is ancient.
    vi.setSystemTime(Date.now() + 10 * 60_000);
    expect(useLiveStore.getState().offlineSince).toBeLessThan(Date.now() - 2_000);

    // Back on screen: a socket is dialed at once and the banner waits a fresh 2 s.
    setTabState("visible");
    expect(FakeSocket.instances).toHaveLength(3);
    expect(result.current).toBe(false);
    act(() => vi.advanceTimersByTime(1_900));
    expect(result.current).toBe(false);
    act(() => vi.advanceTimersByTime(200));
    expect(result.current).toBe(true);

    act(() => FakeSocket.latest().open());
    expect(result.current).toBe(false);
  });

  it("shows the banner 2 s after a visible page loses the line", () => {
    const { result } = renderHook(() => useDisconnected());
    act(() => FakeSocket.latest().drop());
    act(() => vi.advanceTimersByTime(1_999));
    expect(result.current).toBe(false);
    act(() => vi.advanceTimersByTime(1));
    expect(result.current).toBe(true);
  });
});
