import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { enterSends, isTouchFirst, useEnterSends } from "@/hooks/use-enter-sends";
import { stubMatchMedia, stubPointer } from "@/test/media";

/*
 * What a touch device is (ENT-01, ENT-02): the main pointer is coarse or it cannot hover. Enter
 * sends the message everywhere else.
 */

afterEach(() => vi.unstubAllGlobals());

/** A `matchMedia` whose answers change while a component is mounted, and tells who listens. */
function stubLiveMedia(initial: { coarse: boolean; noHover: boolean }) {
  const state = { ...initial };
  const listeners = new Set<() => void>();
  vi.stubGlobal("matchMedia", (query: string) => ({
    media: query,
    get matches() {
      if (query === "(pointer: coarse)") return state.coarse;
      if (query === "(hover: none)") return state.noHover;
      return false;
    },
    addEventListener: (_type: string, listener: () => void) => listeners.add(listener),
    removeEventListener: (_type: string, listener: () => void) => listeners.delete(listener),
  }));
  return {
    listeners,
    set(next: Partial<typeof initial>) {
      Object.assign(state, next);
      for (const listener of [...listeners]) listener();
    },
  };
}

describe("isTouchFirst", () => {
  it("is true when the main pointer is coarse (a finger)", () => {
    stubPointer("coarse");
    expect(isTouchFirst()).toBe(true);
  });

  it("is true when the device cannot hover, whatever its pointer says", () => {
    stubMatchMedia((query) => query === "(hover: none)");
    expect(isTouchFirst()).toBe(true);
  });

  it("is false with a mouse or a trackpad", () => {
    stubMatchMedia((query) => query === "(pointer: fine)" || query === "(hover: hover)");
    expect(isTouchFirst()).toBe(false);
  });

  it("is false on a touch laptop whose main pointer is its trackpad", () => {
    // `any-pointer: coarse` is true as soon as one touch screen is attached; only the main pointer counts.
    stubMatchMedia((query) =>
      ["(any-pointer: coarse)", "(any-hover: none)", "(pointer: fine)", "(hover: hover)"].includes(
        query,
      ),
    );
    expect(isTouchFirst()).toBe(false);
  });

  it("is false where matchMedia does not exist", () => {
    vi.stubGlobal("matchMedia", undefined);
    expect(isTouchFirst()).toBe(false);
  });
});

describe("enterSends", () => {
  it("is the opposite: Enter sends unless the device is touch-first", () => {
    stubPointer("fine");
    expect(enterSends()).toBe(true);
    stubPointer("coarse");
    expect(enterSends()).toBe(false);
    stubMatchMedia((query) => query === "(hover: none)");
    expect(enterSends()).toBe(false);
  });
});

describe("useEnterSends", () => {
  it("starts from what the device is", () => {
    stubPointer("coarse");
    expect(renderHook(() => useEnterSends()).result.current).toBe(false);
    stubPointer("fine");
    expect(renderHook(() => useEnterSends()).result.current).toBe(true);
  });

  it("follows the device when its main pointer changes, such as a convertible switching modes", () => {
    const media = stubLiveMedia({ coarse: false, noHover: false });
    const { result } = renderHook(() => useEnterSends());
    expect(result.current).toBe(true);

    act(() => media.set({ coarse: true, noHover: true }));
    expect(result.current).toBe(false);
    act(() => media.set({ coarse: false, noHover: false }));
    expect(result.current).toBe(true);
  });

  it("stops listening once it unmounts", () => {
    const media = stubLiveMedia({ coarse: false, noHover: false });
    const { unmount } = renderHook(() => useEnterSends());
    expect(media.listeners.size).toBeGreaterThan(0);
    unmount();
    expect(media.listeners.size).toBe(0);
  });
});
