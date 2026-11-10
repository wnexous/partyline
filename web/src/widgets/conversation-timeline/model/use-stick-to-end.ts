import { useCallback, useLayoutEffect, useRef, useState } from "react";

/*
 * The one owner of "the timeline stays at its newest message" (KBD-02). A reader at the end stays
 * there whatever changes size: the on-screen keyboard opens or closes under the composer, the
 * window is resized, the system font grows, the header gets taller, a picture loads, a message
 * arrives. A reader who scrolled up keeps their place.
 *
 * Only the reader decides where they are. Being at the end (within `threshold`) holds the timeline
 * there, and only a move up away from it lets go (or `release`, for a jump to an older message).
 * The scrolls a change of size causes (the browser clamping a taller box to its end, the
 * virtualizer moving the rows down as the ones above grow, this hook's own writes) move down or
 * land at the end, so they are not read as the reader leaving: the decision a size change acts on
 * is the one the reader made before it.
 *
 * Nothing else may scroll to the end on its own (the virtualizer runs with its end-following off):
 * two writers aiming at two ideas of the end, read back by one scroll handler, are what left the
 * timeline above its newest message with "Jump to latest" showing after the keyboard opened.
 */

export interface StickToEnd {
  /** The timeline is held at its newest message: no "Jump to latest". */
  atEnd: boolean;
  /** Goes to the newest message and stays there (Jump to latest). */
  stick: () => void;
  /** Lets go of the end before a jump to an older message (a reply, an image, a focus). */
  release: () => void;
}

/**
 * Holds `scroller` at its end while the reader is there, watching the box and `content` (what it
 * scrolls) for any change of size. Pass null until the elements exist; the timeline starts held.
 */
export function useStickToEnd(
  scroller: HTMLElement | null,
  content: HTMLElement | null,
  threshold: number,
): StickToEnd {
  const [atEnd, setAtEndState] = useState(true);
  const held = useRef(true);
  const box = useRef<HTMLElement | null>(null);
  const hold = useCallback((value: boolean) => {
    held.current = value;
    setAtEndState(value);
  }, []);

  useLayoutEffect(() => {
    box.current = scroller;
    if (!scroller) return;
    const follow = () => {
      if (held.current) scroller.scrollTop = scroller.scrollHeight;
    };
    let last = scroller.scrollTop;
    const onScroll = () => {
      const top = scroller.scrollTop;
      if (scroller.scrollHeight - top - scroller.clientHeight <= threshold) hold(true);
      else if (top < last) hold(false);
      last = top;
    };
    scroller.addEventListener("scroll", onScroll, { passive: true });
    follow();
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(follow);
    observer?.observe(scroller);
    if (content) observer?.observe(content);
    return () => {
      scroller.removeEventListener("scroll", onScroll);
      observer?.disconnect();
    };
  }, [scroller, content, threshold, hold]);

  const stick = useCallback(() => {
    hold(true);
    const el = box.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [hold]);
  const release = useCallback(() => hold(false), [hold]);

  return { atEnd, stick, release };
}
