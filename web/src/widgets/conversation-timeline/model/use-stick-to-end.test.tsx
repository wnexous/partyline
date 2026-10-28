import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useStickToEnd } from "../model/use-stick-to-end";

/*
 * A timeline at its newest message stays there when its box changes height (KBD-02): the
 * keyboard opens and closes under the composer, the window is resized. A reader higher up keeps
 * their place.
 */

let observers: FakeResizeObserver[];

class FakeResizeObserver {
  observed: Element[] = [];
  disconnected = false;
  constructor(private readonly callback: ResizeObserverCallback) {
    observers.push(this);
  }
  observe(element: Element) {
    this.observed.push(element);
  }
  unobserve() {}
  disconnect() {
    this.disconnected = true;
  }
  fire() {
    this.callback([], this as unknown as ResizeObserver);
  }
}

/** A box that is `height` px tall now; `resize` changes it and tells the observer. */
function box(height: number) {
  const el = document.createElement("div");
  let current = height;
  Object.defineProperty(el, "clientHeight", { configurable: true, get: () => current });
  return {
    el,
    resize(next: number) {
      current = next;
      for (const observer of observers) observer.fire();
    },
  };
}

beforeEach(() => {
  observers = [];
  vi.stubGlobal("ResizeObserver", FakeResizeObserver);
});
afterEach(() => vi.unstubAllGlobals());

describe("useStickToEnd (KBD-02)", () => {
  it("goes back to the end when the box shrinks while it was at the end", () => {
    const scrollToEnd = vi.fn();
    const { el, resize } = box(600);
    renderHook(() => useStickToEnd(el, () => true, scrollToEnd));
    resize(318);
    expect(scrollToEnd).toHaveBeenCalledTimes(1);
  });

  it("goes back to the end when the box grows again, as the keyboard closes", () => {
    const scrollToEnd = vi.fn();
    const { el, resize } = box(318);
    renderHook(() => useStickToEnd(el, () => true, scrollToEnd));
    resize(600);
    expect(scrollToEnd).toHaveBeenCalledTimes(1);
  });

  it("leaves a reader who is higher up where they are", () => {
    const scrollToEnd = vi.fn();
    const { el, resize } = box(600);
    renderHook(() => useStickToEnd(el, () => false, scrollToEnd));
    resize(318);
    resize(600);
    expect(scrollToEnd).not.toHaveBeenCalled();
  });

  it("asks where the reader is when the box changes, not when the page mounted", () => {
    let atEnd = true;
    const scrollToEnd = vi.fn();
    const { el, resize } = box(600);
    renderHook(() => useStickToEnd(el, () => atEnd, scrollToEnd));
    atEnd = false;
    resize(318);
    expect(scrollToEnd).not.toHaveBeenCalled();
    atEnd = true;
    resize(600);
    expect(scrollToEnd).toHaveBeenCalledTimes(1);
  });

  it("does nothing when the observer reports a box that is as tall as before (its width changed)", () => {
    const scrollToEnd = vi.fn();
    const { el, resize } = box(600);
    renderHook(() => useStickToEnd(el, () => true, scrollToEnd));
    resize(600);
    expect(scrollToEnd).not.toHaveBeenCalled();
  });

  it("does nothing without a box yet (the timeline is still loading)", () => {
    const scrollToEnd = vi.fn();
    renderHook(() => useStickToEnd(null, () => true, scrollToEnd));
    expect(observers).toHaveLength(0);
  });

  it("watches the box that appears later, and lets the old one go", () => {
    const scrollToEnd = vi.fn();
    const first = box(600);
    const second = box(500);
    const { rerender } = renderHook(
      ({ el }: { el: HTMLElement | null }) => useStickToEnd(el, () => true, scrollToEnd),
      { initialProps: { el: null as HTMLElement | null } },
    );
    rerender({ el: first.el });
    rerender({ el: second.el });
    expect(observers[0]?.disconnected).toBe(true);
    expect(observers[1]?.observed).toEqual([second.el]);
  });

  it("stops watching when the page goes", () => {
    const { el } = box(600);
    const { unmount } = renderHook(() => useStickToEnd(el, () => true, vi.fn()));
    unmount();
    expect(observers[0]?.disconnected).toBe(true);
  });

  it("uses the latest callbacks without watching the box again", () => {
    const first = vi.fn();
    const second = vi.fn();
    const { el, resize } = box(600);
    const { rerender } = renderHook(
      ({ go }: { go: () => void }) => useStickToEnd(el, () => true, go),
      { initialProps: { go: first } },
    );
    rerender({ go: second });
    expect(observers).toHaveLength(1);
    resize(300);
    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledTimes(1);
  });
});
