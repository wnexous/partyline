import { useEffect } from "react";
import { visibleArea } from "@/shared/lib/viewport";

/*
 * Publishes what the user can see as `--app-height` and `--app-top` on <html> (KBD-01), for the
 * full-height shell to size itself with. With the on-screen keyboard open the page is then as
 * tall as the room left above it, whichever way the browser treats the keyboard. Until the first
 * frame, and in a browser without a visual viewport, the stylesheet falls back to `100dvh`.
 */

const HEIGHT = "--app-height";
const TOP = "--app-top";

/** Follows the visual viewport for as long as the component is mounted. Mount it once, in the shell. */
export function useVisualViewport() {
  useEffect(() => {
    const viewport = window.visualViewport;
    if (!viewport) return;
    const root = document.documentElement;
    let frame = 0;

    const apply = () => {
      frame = 0;
      const area = visibleArea(viewport);
      if (area) {
        root.style.setProperty(HEIGHT, `${area.height}px`);
        root.style.setProperty(TOP, `${area.top}px`);
      } else {
        root.style.removeProperty(HEIGHT);
        root.style.removeProperty(TOP);
      }
    };
    // A keyboard animates for a few hundred milliseconds, firing an event per frame at best: one
    // write per frame is all the page can show.
    const schedule = () => {
      if (frame === 0) frame = requestAnimationFrame(apply);
    };

    apply();
    viewport.addEventListener("resize", schedule);
    viewport.addEventListener("scroll", schedule);
    return () => {
      viewport.removeEventListener("resize", schedule);
      viewport.removeEventListener("scroll", schedule);
      if (frame !== 0) cancelAnimationFrame(frame);
      root.style.removeProperty(HEIGHT);
      root.style.removeProperty(TOP);
    };
  }, []);
}
