import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useStickToEnd } from "../model/use-stick-to-end";

/*
 * A timeline at its newest message stays there whatever changes size (KBD-02): the keyboard opens
 * and closes under the composer, the window is resized, the font grows, a picture loads. Only the
 * reader moving up lets go of the end; a reader higher up keeps their place.
 */

let observers: FakeResizeObserver[];

class FakeResizeObserver {
  observed: Element[] = [];
  disconnected = false;
  constructor(readonly callback: ResizeObserverCallback) {
    observers.push(this);
  }
  observe(element: Element) {
    this.observed.push(element);
  }
  unobserve() {}
  disconnect() {
    this.disconnected = true;
  }
}

/** Tells the observers watching `element` that it changed size. */
function resized(element: Element) {
  act(() => {
    for (const observer of observers) {
      if (!observer.disconnected && observer.observed.includes(element)) {
        observer.callback([], observer as unknown as ResizeObserver);
      }
    }
  });
}

/**
 * A scrolling box as a browser lays it out: `height` px tall over `scrollHeight` px of content,
 * scrollTop clamped to what can scroll, and a scroll event whenever it moves, whoever moved it.
 * `scrollTo` is the reader's scroll, `written` one the page writes (the virtualizer keeping its
 * rows in place), `layout` a change of size (the keyboard, the font, a picture, a new message).
 */
function box(height: number, scrollHeight: number) {
  const el = document.createElement("div");
  const content = document.createElement("div");
  el.append(content);
  let client = height;
  let total = scrollHeight;
  let top = 0;
  const clamp = (value: number) => Math.max(0, Math.min(value, total - client));
  Object.defineProperty(el, "clientHeight", { configurable: true, get: () => client });
  Object.defineProperty(el, "scrollHeight", { configurable: true, get: () => total });
  Object.defineProperty(el, "scrollTop", {
    configurable: true,
    get: () => top,
    set: (value: number) => move(value),
  });
  const fire = () => act(() => void el.dispatchEvent(new Event("scroll")));
  function move(value: number) {
    const next = clamp(value);
    if (next === top) return;
    top = next;
    fire();
  }
  return {
    el,
    content,
    distance: () => total - top - client,
    top: () => top,
    scrollTo: move,
    written: move,
    /**
     * New sizes; the browser clamps the position when the end moved above it. The observers hear
     * of it unless `observed` is false (they are told later, as a browser does in its next frame).
     */
    layout(next: { height?: number; scrollHeight?: number }, observed = true) {
      client = next.height ?? client;
      total = next.scrollHeight ?? total;
      move(top);
      if (!observed) return;
      if (next.height !== undefined) resized(el);
      if (next.scrollHeight !== undefined) resized(content);
    },
    observe: () => {
      resized(el);
      resized(content);
    },
  };
}

const THRESHOLD = 64;

beforeEach(() => {
  observers = [];
  vi.stubGlobal("ResizeObserver", FakeResizeObserver);
});
afterEach(() => vi.unstubAllGlobals());

describe("useStickToEnd (KBD-02)", () => {
  it("starts at the end", () => {
    const b = box(600, 2000);
    const { result } = renderHook(() => useStickToEnd(b.el, b.content, THRESHOLD));
    expect(b.distance()).toBe(0);
    expect(result.current.atEnd).toBe(true);
  });

  it("stays at the end when the keyboard takes room from the box, and when it gives it back", () => {
    const b = box(600, 2000);
    const { result } = renderHook(() => useStickToEnd(b.el, b.content, THRESHOLD));
    b.layout({ height: 318 });
    expect(b.distance()).toBe(0);
    b.layout({ height: 600 });
    expect(b.distance()).toBe(0);
    expect(result.current.atEnd).toBe(true);
  });

  it("stays at the end when the rows grow (a bigger font, a picture, a new message)", () => {
    const b = box(600, 2000);
    const { result } = renderHook(() => useStickToEnd(b.el, b.content, THRESHOLD));
    b.layout({ scrollHeight: 2600 });
    expect(b.distance()).toBe(0);
    expect(result.current.atEnd).toBe(true);
  });

  it("is not let go by the scrolls a change of size causes, and ends at the end", () => {
    // The font grows: the rows grow, the virtualizer moves the ones above down, the header grows
    // and the box shrinks, all before the observers hear of it. The reader never moved.
    const b = box(600, 2000);
    const { result } = renderHook(() => useStickToEnd(b.el, b.content, THRESHOLD));
    b.layout({ scrollHeight: 2900 }, false);
    b.written(b.top() + 300);
    expect(b.distance()).toBeGreaterThan(THRESHOLD);
    expect(result.current.atEnd).toBe(true);
    b.layout({ height: 500 }, false);
    b.observe();
    expect(b.distance()).toBe(0);
    expect(result.current.atEnd).toBe(true);
    // The window grows again (the keyboard closes): the browser pulls the position up to the new
    // end, and that is no reader leaving it either.
    b.layout({ height: 600 });
    expect(b.distance()).toBe(0);
    expect(result.current.atEnd).toBe(true);
  });

  it("counts a reader a little above the end as at it", () => {
    const b = box(600, 2000);
    const { result } = renderHook(() => useStickToEnd(b.el, b.content, THRESHOLD));
    b.scrollTo(1400 - 40);
    expect(result.current.atEnd).toBe(true);
    b.layout({ height: 318 });
    expect(b.distance()).toBe(0);
  });

  it("lets go when the reader scrolls up, and keeps their place through a change of size", () => {
    const b = box(600, 2000);
    const { result } = renderHook(() => useStickToEnd(b.el, b.content, THRESHOLD));
    b.scrollTo(500);
    expect(result.current.atEnd).toBe(false);
    b.layout({ height: 318 });
    b.layout({ scrollHeight: 2400 });
    b.layout({ height: 600 });
    expect(b.top()).toBe(500);
    expect(result.current.atEnd).toBe(false);
  });

  it("holds again once the reader comes back to the end", () => {
    const b = box(600, 2000);
    const { result } = renderHook(() => useStickToEnd(b.el, b.content, THRESHOLD));
    b.scrollTo(500);
    b.scrollTo(1000);
    expect(result.current.atEnd).toBe(false);
    b.scrollTo(1400);
    expect(result.current.atEnd).toBe(true);
    b.layout({ height: 318 });
    expect(b.distance()).toBe(0);
  });

  it("goes to the end and holds it on stick (Jump to latest)", () => {
    const b = box(600, 2000);
    const { result } = renderHook(() => useStickToEnd(b.el, b.content, THRESHOLD));
    b.scrollTo(200);
    act(() => result.current.stick());
    expect(b.distance()).toBe(0);
    expect(result.current.atEnd).toBe(true);
    b.layout({ scrollHeight: 2500 });
    expect(b.distance()).toBe(0);
  });

  it("lets go on release, before a jump to an older message", () => {
    const b = box(600, 2000);
    const { result } = renderHook(() => useStickToEnd(b.el, b.content, THRESHOLD));
    act(() => result.current.release());
    b.written(700);
    b.layout({ height: 318 });
    expect(b.top()).toBe(700);
    expect(result.current.atEnd).toBe(false);
  });

  it("does nothing without a box yet (the timeline is still loading)", () => {
    const { result } = renderHook(() => useStickToEnd(null, null, THRESHOLD));
    expect(observers).toHaveLength(0);
    expect(result.current.atEnd).toBe(true);
    act(() => result.current.stick());
  });

  it("watches the box that appears later, and lets the old one go", () => {
    const first = box(600, 2000);
    const second = box(500, 1500);
    const { rerender } = renderHook(
      ({ b }: { b: ReturnType<typeof box> | null }) =>
        useStickToEnd(b?.el ?? null, b?.content ?? null, THRESHOLD),
      { initialProps: { b: null as ReturnType<typeof box> | null } },
    );
    rerender({ b: first });
    rerender({ b: second });
    expect(observers[0]?.disconnected).toBe(true);
    expect(observers[1]?.observed).toEqual([second.el, second.content]);
    expect(second.distance()).toBe(0);
  });

  it("stops watching when the page goes", () => {
    const b = box(600, 2000);
    const { unmount } = renderHook(() => useStickToEnd(b.el, b.content, THRESHOLD));
    unmount();
    expect(observers[0]?.disconnected).toBe(true);
  });
});
