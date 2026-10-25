import { type MouseEvent, type PointerEvent, useEffect, useMemo, useRef } from "react";

/*
 * What opens the actions of a row (ACT-01, ACT-02). A finger or a pen that rests on it for
 * LONG_PRESS_MS, moving at most LONG_PRESS_SLOP_PX, opens them where it is; a tap, a scroll, a drag
 * and a release that comes early do not. The click that ends a long press is swallowed, or it
 * would open the conversation the row links to. The browser's own context menu (a right click, the
 * context-menu key, Shift+F10, Android's long press, a screen reader's long click) is turned into
 * the same call and never shown.
 */

/** Chrome's own long press is about as long, so ours is there first. */
export const LONG_PRESS_MS = 500;
/** How far a resting finger may drift and still be resting. */
export const LONG_PRESS_SLOP_PX = 10;

/** The click that follows a long press comes within this long of the finger lifting. */
const SWALLOW_MS = 500;
/** A long press and the browser's context menu for the same press arrive this close together. */
const SAME_PRESS_MS = 1_000;

export interface PressPoint<T extends HTMLElement> {
  /** Viewport position: where the finger or the pointer is; the row's bottom-left for the keyboard. */
  x: number;
  y: number;
  /** The row that was pressed; focus goes back to it when the actions close. */
  element: T;
}

interface Press {
  id: number;
  x: number;
  y: number;
  timer: number;
  fired: boolean;
}

export function useLongPress<T extends HTMLElement>(
  open: (where: PressPoint<T>) => void,
  { enabled = true }: { enabled?: boolean } = {},
) {
  const latest = useRef(open);
  latest.current = open;
  const press = useRef<Press | null>(null);
  // Set when a long press opened the actions: the click that ends it is not the row's.
  const swallow = useRef(false);
  const swallowTimer = useRef(0);
  // When our own timer last opened the actions, and what kind of pointer was down for it: the
  // browser's context menu that follows the same long press must not open them a second time.
  const firedAt = useRef(Number.NEGATIVE_INFINITY);
  const lastPointer = useRef("");

  useEffect(
    () => () => {
      window.clearTimeout(press.current?.timer);
      window.clearTimeout(swallowTimer.current);
    },
    [],
  );

  return useMemo(() => {
    const forget = () => {
      if (!press.current) return;
      window.clearTimeout(press.current.timer);
      press.current = null;
    };
    const opened = (where: PressPoint<T>) => latest.current(where);
    /** The finger is up (or was taken): its click, if one comes, follows now. */
    const lifted = (e: PointerEvent<T>) => {
      const p = press.current;
      if (!p || p.id !== e.pointerId) return;
      press.current = null;
      window.clearTimeout(p.timer);
      if (p.fired) {
        window.clearTimeout(swallowTimer.current);
        swallowTimer.current = window.setTimeout(() => {
          swallow.current = false;
        }, SWALLOW_MS);
      }
    };

    return {
      onPointerDown(e: PointerEvent<T>) {
        window.clearTimeout(swallowTimer.current);
        swallow.current = false;
        lastPointer.current = e.pointerType;
        if (!enabled || e.pointerType === "mouse" || !e.isPrimary || e.button !== 0) return;
        forget();
        const element = e.currentTarget;
        const next: Press = {
          id: e.pointerId,
          x: e.clientX,
          y: e.clientY,
          fired: false,
          timer: window.setTimeout(() => {
            next.fired = true;
            swallow.current = true;
            firedAt.current = Date.now();
            opened({ x: next.x, y: next.y, element });
          }, LONG_PRESS_MS),
        };
        press.current = next;
      },
      onPointerMove(e: PointerEvent<T>) {
        const p = press.current;
        if (!p || p.fired || p.id !== e.pointerId) return;
        if (Math.hypot(e.clientX - p.x, e.clientY - p.y) > LONG_PRESS_SLOP_PX) forget();
      },
      onPointerUp: lifted,
      onPointerCancel: lifted,
      onClickCapture(e: MouseEvent<T>) {
        if (!swallow.current) return;
        swallow.current = false;
        e.preventDefault();
        e.stopPropagation();
      },
      onContextMenu(e: MouseEvent<T>) {
        if (!enabled) return;
        // The browser's menu stays closed, whoever asked for it.
        e.preventDefault();
        // The browser's own menu for a long press that ours already answered: once is enough. A
        // mouse is always a new press, however soon it follows another.
        const ours =
          lastPointer.current !== "mouse" && Date.now() - firedAt.current < SAME_PRESS_MS;
        if (ours) return;
        const element = e.currentTarget;
        // From the keyboard there is no position: the menu goes under the row.
        const keyboard = e.clientX === 0 && e.clientY === 0;
        const box = element.getBoundingClientRect();
        // A long press the browser recognised first is ours too, and so is the click that ends it.
        if (press.current) {
          press.current.fired = true;
          swallow.current = true;
        }
        opened({
          x: keyboard ? box.left : e.clientX,
          y: keyboard ? box.bottom : e.clientY,
          element,
        });
      },
    };
  }, [enabled]);
}
