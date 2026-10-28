import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { useLiveStore } from "@/test/live-store";
import { useIsOnline } from "./presence";

beforeEach(() => useLiveStore.setState({ presence: {} }));

describe("useIsOnline", () => {
  it("falls back to the given value, then to offline", () => {
    expect(renderHook(() => useIsOnline("a1", true)).result.current).toBe(true);
    expect(renderHook(() => useIsOnline("a1", false)).result.current).toBe(false);
    expect(renderHook(() => useIsOnline("a1")).result.current).toBe(false);
  });

  it("lets the live presence win, and follows it", () => {
    const { result } = renderHook(() => useIsOnline("a1", true));
    act(() => useLiveStore.getState().setPresence("a1", { online: false }));
    expect(result.current).toBe(false);
    act(() => useLiveStore.getState().setPresence("a1", { online: true }));
    expect(result.current).toBe(true);
  });

  it("ignores the presence of someone else", () => {
    useLiveStore.getState().setPresence("a2", { online: true });
    expect(renderHook(() => useIsOnline("a1", false)).result.current).toBe(false);
  });
});
