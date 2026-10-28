import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useVisualViewport } from "@/shared/lib/use-visual-viewport";

/*
 * The page follows what the user can see (KBD-01): the hook publishes the visual viewport as two
 * custom properties on <html>, which the full-height shell sizes itself with.
 */

class FakeViewport extends EventTarget {
  height = 640;
  offsetTop = 0;
  scale = 1;

  move(over: Partial<Pick<FakeViewport, "height" | "offsetTop" | "scale">>, event = "resize") {
    Object.assign(this, over);
    this.dispatchEvent(new Event(event));
  }

  listeners = 0;
  override addEventListener(...args: Parameters<EventTarget["addEventListener"]>) {
    this.listeners += 1;
    super.addEventListener(...args);
  }
  override removeEventListener(...args: Parameters<EventTarget["removeEventListener"]>) {
    this.listeners -= 1;
    super.removeEventListener(...args);
  }
}

const root = () => document.documentElement;
const height = () => root().style.getPropertyValue("--app-height");
const top = () => root().style.getPropertyValue("--app-top");

let viewport: FakeViewport;
let frames: FrameRequestCallback[];

/** Runs the animation frames asked for so far. */
const flushFrames = () =>
  act(() => {
    const pending = frames.splice(0);
    for (const frame of pending) frame(0);
  });

beforeEach(() => {
  viewport = new FakeViewport();
  frames = [];
  vi.stubGlobal("visualViewport", viewport);
  vi.stubGlobal("requestAnimationFrame", (frame: FrameRequestCallback) => frames.push(frame));
  vi.stubGlobal("cancelAnimationFrame", (id: number) => {
    frames[id - 1] = () => {};
  });
});
afterEach(() => {
  vi.unstubAllGlobals();
  root().style.removeProperty("--app-height");
  root().style.removeProperty("--app-top");
});

describe("useVisualViewport (KBD-01)", () => {
  it("publishes the visible area as soon as the page mounts", () => {
    viewport.height = 702;
    renderHook(() => useVisualViewport());
    expect(height()).toBe("702px");
    expect(top()).toBe("0px");
  });

  it("follows the keyboard: the area shrinks when it opens and grows back when it closes", () => {
    renderHook(() => useVisualViewport());
    expect(height()).toBe("640px");

    viewport.move({ height: 318 });
    flushFrames();
    expect(height()).toBe("318px");

    viewport.move({ height: 640 });
    flushFrames();
    expect(height()).toBe("640px");
  });

  it("follows the page a browser pans to keep the focused field in view", () => {
    renderHook(() => useVisualViewport());
    viewport.move({ height: 318, offsetTop: 96 }, "scroll");
    flushFrames();
    expect(height()).toBe("318px");
    expect(top()).toBe("96px");
  });

  it("writes once per frame, whatever the number of events in it", () => {
    renderHook(() => useVisualViewport());
    const set = vi.spyOn(root().style, "setProperty");
    viewport.move({ height: 500 });
    viewport.move({ height: 400 }, "scroll");
    viewport.move({ height: 330 });
    expect(frames.filter(Boolean)).toHaveLength(1);
    expect(height()).toBe("640px");
    flushFrames();
    expect(set).toHaveBeenCalledTimes(2);
    expect(height()).toBe("330px");
  });

  it("leaves the layout alone while the page is pinch-zoomed, and takes it up again after", () => {
    renderHook(() => useVisualViewport());
    viewport.move({ scale: 2, height: 320 });
    flushFrames();
    expect(height()).toBe("");
    expect(top()).toBe("");

    viewport.move({ scale: 1, height: 640 });
    flushFrames();
    expect(height()).toBe("640px");
  });

  it("does nothing, and does not fail, in a browser without a visual viewport", () => {
    vi.stubGlobal("visualViewport", undefined);
    expect(() => renderHook(() => useVisualViewport())).not.toThrow();
    expect(height()).toBe("");
  });

  it("gives the layout back to the stylesheet and stops listening when the page goes", () => {
    const { unmount } = renderHook(() => useVisualViewport());
    expect(viewport.listeners).toBe(2);
    viewport.move({ height: 300 });
    unmount();
    expect(height()).toBe("");
    expect(top()).toBe("");
    expect(viewport.listeners).toBe(0);
    // The frame asked for before it went must not write to the page afterwards.
    flushFrames();
    expect(height()).toBe("");
  });
});
