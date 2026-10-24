import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { visibleArea } from "@/lib/viewport";

/*
 * The area of the page the user can see (KBD-01). The on-screen keyboard shrinks the visual
 * viewport in every browser; only some also shrink the layout viewport.
 */

const vv = (over: Partial<Pick<VisualViewport, "height" | "offsetTop" | "scale">> = {}) => ({
  height: 640,
  offsetTop: 0,
  scale: 1,
  ...over,
});

describe("visibleArea (KBD-01)", () => {
  it("is the visual viewport: its height, and how far it sits below the top of the page", () => {
    expect(visibleArea(vv())).toEqual({ height: 640, top: 0 });
    expect(visibleArea(vv({ height: 318, offsetTop: 42 }))).toEqual({ height: 318, top: 42 });
  });

  it("is whole pixels, so a border never lands between two", () => {
    expect(visibleArea(vv({ height: 318.4, offsetTop: 41.6 }))).toEqual({ height: 318, top: 42 });
    expect(visibleArea(vv({ height: 318.5 }))).toEqual({ height: 319, top: 0 });
  });

  it("is nothing while the page is pinch-zoomed: a zoomed viewport is small without any keyboard", () => {
    expect(visibleArea(vv({ scale: 2, height: 320 }))).toBeNull();
    expect(visibleArea(vv({ scale: 1.25 }))).toBeNull();
    expect(visibleArea(vv({ scale: 0.8 }))).toBeNull();
  });

  it("calls a scale within a hundredth of 1 unzoomed (browsers report 1.0000001)", () => {
    expect(visibleArea(vv({ scale: 1.005 }))).toEqual({ height: 640, top: 0 });
    expect(visibleArea(vv({ scale: 0.995 }))).toEqual({ height: 640, top: 0 });
    expect(visibleArea(vv({ scale: 1.011 }))).toBeNull();
  });

  it("is nothing without a visual viewport (the browser has none)", () => {
    expect(visibleArea(null)).toBeNull();
    expect(visibleArea(undefined)).toBeNull();
  });

  it("is nothing for a viewport with no height: a hidden page reports 0 until it is shown again", () => {
    expect(visibleArea(vv({ height: 0 }))).toBeNull();
  });
});

// A variable, not a literal: Vite rewrites `new URL("literal", import.meta.url)` into an asset URL.
const here = (relative: string) => fileURLToPath(new URL(relative, import.meta.url));

describe("the viewport meta (KBD-05)", () => {
  const html = readFileSync(here("../../index.html"), "utf8");
  const content = /<meta\s+name="viewport"\s+content="([^"]*)"/.exec(html)?.[1] ?? "";

  it("asks Chrome to resize the page for the on-screen keyboard", () => {
    expect(content.split(",").map((part) => part.trim())).toContain(
      "interactive-widget=resizes-content",
    );
  });

  it("keeps the width and the scale it always had: it is no reason to zoom the page", () => {
    const parts = content.split(",").map((part) => part.trim());
    expect(parts).toContain("width=device-width");
    expect(parts).toContain("initial-scale=1.0");
    expect(parts.join(",")).not.toMatch(/maximum-scale|user-scalable/);
  });
});
