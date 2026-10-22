import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useDebouncedValue } from "./use-debounced-value";

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe("useDebouncedValue", () => {
  it("starts with the value it is given", () => {
    const { result } = renderHook(() => useDebouncedValue("lia", 250));
    expect(result.current).toBe("lia");
  });

  it("follows a change only once it has stopped changing for the delay", () => {
    const { result, rerender } = renderHook(({ value }) => useDebouncedValue(value, 250), {
      initialProps: { value: "" },
    });
    rerender({ value: "l" });
    rerender({ value: "li" });
    act(() => vi.advanceTimersByTime(200));
    rerender({ value: "lia" });
    act(() => vi.advanceTimersByTime(200));
    // 400 ms in, but the last change was 200 ms ago: nothing settled yet.
    expect(result.current).toBe("");
    act(() => vi.advanceTimersByTime(50));
    expect(result.current).toBe("lia");
  });

  it("does not wait at all for the value it already settled on", () => {
    const { result, rerender } = renderHook(({ value }) => useDebouncedValue(value, 250), {
      initialProps: { value: "a" },
    });
    rerender({ value: "a" });
    expect(result.current).toBe("a");
  });
});
