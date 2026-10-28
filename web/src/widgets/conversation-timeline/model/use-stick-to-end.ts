import { useEffect, useRef } from "react";

/*
 * A scrolling box that was at its end stays there when its height changes (KBD-02): the on-screen
 * keyboard opens and closes under a composer, a window is resized. The browser keeps the scroll
 * position instead, which leaves the newest message below the new edge. A reader who is higher up
 * keeps their place.
 */

/**
 * Calls `scrollToEnd` whenever `element` changes height while `atEnd()` says the reader was at its
 * end. `atEnd` is asked when the height changes, so it must describe the box as the reader left it
 * (the last scroll), not as the resize made it. Pass null until the element exists.
 */
export function useStickToEnd(
  element: HTMLElement | null,
  atEnd: () => boolean,
  scrollToEnd: () => void,
) {
  const latest = useRef({ atEnd, scrollToEnd });
  useEffect(() => {
    latest.current = { atEnd, scrollToEnd };
  });

  useEffect(() => {
    if (!element || typeof ResizeObserver === "undefined") return;
    let height = element.clientHeight;
    const observer = new ResizeObserver(() => {
      const next = element.clientHeight;
      if (next === height) return;
      height = next;
      if (latest.current.atEnd()) latest.current.scrollToEnd();
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [element]);
}
