import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLiveStore } from "@/test/live-store";

/*
 * The store's outage clock (UI-06): when the live socket has not been open since. The banner
 * measures its 2 s grace from it, so it must mark the first drop and only restart when the page
 * comes back (a hidden tab's timestamp is minutes old by then).
 */

describe("live store outage clock (UI-06)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-10-19T12:00:00Z"));
    useLiveStore.setState({ status: "closed", offlineSince: null });
  });
  afterEach(() => {
    useLiveStore.setState({ status: "closed", offlineSince: null });
    vi.useRealTimers();
  });

  it("starts the clock at the first drop and keeps it through the retries", () => {
    useLiveStore.getState().setStatus("open");
    const droppedAt = Date.now();
    useLiveStore.getState().setStatus("closed");
    expect(useLiveStore.getState().offlineSince).toBe(droppedAt);

    // Retries flip between connecting and closed; the outage started at the first drop.
    vi.setSystemTime(droppedAt + 20_000);
    useLiveStore.getState().setStatus("connecting");
    useLiveStore.getState().setStatus("closed");
    expect(useLiveStore.getState().offlineSince).toBe(droppedAt);
  });

  it("clears the clock as soon as the line is open", () => {
    useLiveStore.getState().setStatus("open");
    useLiveStore.getState().setStatus("closed");
    expect(useLiveStore.getState().offlineSince).not.toBeNull();
    useLiveStore.getState().setStatus("open");
    expect(useLiveStore.getState().offlineSince).toBeNull();
  });

  it("restarts the clock when a hidden page comes back and the line is still down", () => {
    useLiveStore.getState().setStatus("open");
    useLiveStore.getState().setStatus("closed");
    vi.setSystemTime(Date.now() + 10 * 60_000);
    useLiveStore.getState().markVisible();
    expect(useLiveStore.getState().offlineSince).toBe(Date.now());
  });

  it("leaves an open line and an idle store alone", () => {
    useLiveStore.getState().setStatus("open");
    useLiveStore.getState().markVisible();
    expect(useLiveStore.getState().offlineSince).toBeNull();

    useLiveStore.setState({ status: "closed", offlineSince: null });
    useLiveStore.getState().markVisible();
    expect(useLiveStore.getState().offlineSince).toBeNull();
  });
});
