import { type PointerEvent, useEffect, useMemo, useRef } from "react";

/*
 * Answering a message with a drag to the right, as in the messengers of a phone (SWP-01..SWP-04).
 * The row follows the finger, a reply icon appears as it comes, and a light tick marks the moment it
 * will answer; let go past SWIPE_REPLY_PX it does, let go before it springs back and answers
 * nothing. The page keeps scrolling (`touch-action: pan-y` on the row): a drag that is mostly
 * vertical is the browser's and never starts this, and once this has started the browser does not
 * scroll. It starts from neither a link, a button, the player nor anything marked `data-no-swipe`,
 * and a finger that rested first is a press, not a swipe.
 *
 * The row is moved through its own style, one write per frame: React is not asked to render for
 * every pixel the finger covers.
 */

/** How far the finger goes before it is a swipe or a scroll. */
export const SWIPE_START_PX = 10;
/** How far it must have gone to answer. */
export const SWIPE_REPLY_PX = 60;
/** The row never goes further than this: past SWIPE_REPLY_PX it follows with resistance. */
export const SWIPE_MAX_PX = 96;
/** A finger that rests this long before moving is a long press. */
const REST_MS = 500;
const SPRING_MS = 180;
const RESISTANCE = 0.35;

const INTERACTIVE =
  'a, button, input, textarea, select, audio, video, [role="slider"], [data-no-swipe]';

interface Gesture {
  id: number;
  element: HTMLElement;
  x0: number;
  y0: number;
  at: number;
  /** It is a swipe: the row is moving. */
  started: boolean;
  /** It is something else (a scroll, a swipe to the left, a press): it is left alone. */
  ignored: boolean;
  armed: boolean;
  dx: number;
  frame: number;
}

const prefersLessMotion = () =>
  typeof window.matchMedia === "function" &&
  window.matchMedia("(prefers-reduced-motion: reduce)").matches;

/** How far the row goes for a finger that went `dx`: with it up to the point of answering, then less. */
const follow = (dx: number) =>
  Math.min(
    SWIPE_MAX_PX,
    dx <= SWIPE_REPLY_PX ? dx : SWIPE_REPLY_PX + (dx - SWIPE_REPLY_PX) * RESISTANCE,
  );

function tick() {
  try {
    navigator.vibrate?.(10);
  } catch {
    // A browser that refuses to vibrate is a browser without the tick.
  }
}

export function useSwipeReply<T extends HTMLElement>(
  onReply: () => void,
  { enabled = true }: { enabled?: boolean } = {},
) {
  const latest = useRef(onReply);
  latest.current = onReply;
  const gesture = useRef<Gesture | null>(null);
  const springTimer = useRef(0);

  useEffect(
    () => () => {
      window.clearTimeout(springTimer.current);
      if (gesture.current) cancelAnimationFrame(gesture.current.frame);
    },
    [],
  );

  return useMemo(() => {
    /** Puts the row where the finger has taken it. */
    const draw = (g: Gesture) => {
      g.frame = 0;
      const x = follow(g.dx);
      g.element.style.transform = `translateX(${x}px)`;
      g.element.style.setProperty("--swipe-progress", String(Math.min(1, x / SWIPE_REPLY_PX)));
    };

    /** Gives the row back to where it rests, with or without the trip, and forgets the gesture. */
    const release = (g: Gesture) => {
      cancelAnimationFrame(g.frame);
      gesture.current = null;
      if (!g.started) return;
      const { element } = g;
      element.removeAttribute("data-armed");
      element.style.transform = "";
      element.style.setProperty("--swipe-progress", "0");
      element.style.transition = prefersLessMotion() ? "none" : `transform ${SPRING_MS}ms ease-out`;
      window.clearTimeout(springTimer.current);
      springTimer.current = window.setTimeout(() => {
        element.style.transition = "";
        element.style.removeProperty("--swipe-progress");
      }, SPRING_MS + 40);
    };

    return {
      onPointerDown(e: PointerEvent<T>) {
        if (!enabled || e.pointerType === "mouse" || !e.isPrimary || e.button !== 0) return;
        const from = e.target;
        if (from instanceof Element && from.closest(INTERACTIVE)) return;
        // A row that is still coming back from the last swipe starts this one where it is.
        window.clearTimeout(springTimer.current);
        e.currentTarget.style.transition = "none";
        gesture.current = {
          id: e.pointerId,
          element: e.currentTarget,
          x0: e.clientX,
          y0: e.clientY,
          at: Date.now(),
          started: false,
          ignored: false,
          armed: false,
          dx: 0,
          frame: 0,
        };
      },
      onPointerMove(e: PointerEvent<T>) {
        const g = gesture.current;
        if (!g || g.id !== e.pointerId || g.ignored) return;
        const dx = e.clientX - g.x0;
        const dy = e.clientY - g.y0;
        if (!g.started) {
          if (Math.hypot(dx, dy) < SWIPE_START_PX) return;
          // Decided once, on the first real movement: across and to the right is a swipe; up, down,
          // to the left and after a rest are not.
          if (dx <= 0 || Math.abs(dy) >= Math.abs(dx) || Date.now() - g.at > REST_MS) {
            g.ignored = true;
            return;
          }
          g.started = true;
          try {
            // The finger may leave the row and the row still follows it.
            g.element.setPointerCapture(e.pointerId);
          } catch {
            // A pointer that is gone (a synthetic one): nothing to hold.
          }
        }
        g.dx = Math.max(0, dx);
        const armed = g.dx >= SWIPE_REPLY_PX;
        if (armed !== g.armed) {
          g.armed = armed;
          if (armed) {
            tick();
            g.element.setAttribute("data-armed", "true");
          } else {
            g.element.removeAttribute("data-armed");
          }
        }
        if (!g.frame) g.frame = requestAnimationFrame(() => draw(g));
      },
      onPointerUp(e: PointerEvent<T>) {
        const g = gesture.current;
        if (!g || g.id !== e.pointerId) return;
        const answers = g.started && e.clientX - g.x0 >= SWIPE_REPLY_PX;
        release(g);
        if (answers) latest.current();
      },
      onPointerCancel(e: PointerEvent<T>) {
        const g = gesture.current;
        if (g && g.id === e.pointerId) release(g);
      },
    };
  }, [enabled]);
}
