/*
 * The area of the page the user can see (KBD-01). The on-screen keyboard always shrinks the visual
 * viewport; only some browsers also shrink the layout viewport (Chrome does with
 * `interactive-widget=resizes-content` in the viewport meta, iOS Safari never does). A page that
 * must keep its composer in sight sizes itself to the visual viewport, which is right in all of them.
 */

export interface VisibleArea {
  /** How tall the visible area is, in whole pixels. */
  height: number;
  /** How far its top edge is below the top of the page (a browser pans the page to keep a focused field in view). */
  top: number;
}

/** Browsers report a scale of 1.0000001 for a page nobody zoomed. */
const UNZOOMED = 0.01;

/**
 * The visible area of `viewport`, or null when the layout should not follow it: there is none, it
 * has no height yet (a hidden page reports 0), or the user pinch-zoomed the page, which makes the
 * visual viewport small without any keyboard in the way.
 */
export function visibleArea(
  viewport: Pick<VisualViewport, "height" | "offsetTop" | "scale"> | null | undefined,
): VisibleArea | null {
  if (!viewport || viewport.height <= 0) return null;
  if (Math.abs(viewport.scale - 1) > UNZOOMED) return null;
  return { height: Math.round(viewport.height), top: Math.round(viewport.offsetTop) };
}
