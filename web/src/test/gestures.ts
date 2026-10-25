import { act, fireEvent } from "@testing-library/react";
import { stubMatchMedia } from "@/test/media";

/*
 * Fingers and mice for tests of the gestures of a row (ACT-01, SWP-01): the pointer events a
 * browser sends for a touch, a long press that rests longer than the half second, and the two kinds
 * of device the app tells apart.
 */

/** Longer than LONG_PRESS_MS: a finger that rests this long has pressed. */
export const HOLD = 560;

export const wait = (ms: number) => new Promise((done) => setTimeout(done, ms));

/** The init of a pointer event of a finger. */
export const finger = (over: Partial<PointerEventInit> = {}): PointerEventInit => ({
  pointerType: "touch",
  pointerId: 1,
  isPrimary: true,
  button: 0,
  clientX: 80,
  clientY: 120,
  ...over,
});

/** Rests a finger on `element` for longer than the half second, then lifts it: the click follows. */
export async function longPress(element: HTMLElement, over: Partial<PointerEventInit> = {}) {
  fireEvent.pointerDown(element, finger(over));
  await act(() => wait(HOLD));
  fireEvent.pointerUp(element, finger(over));
  fireEvent.click(element);
}

/** A phone or a tablet: a coarse main pointer that cannot hover. */
export const touchDevice = () =>
  stubMatchMedia(
    (q) =>
      q.includes("pointer: coarse") || q.includes("hover: none") || /max-width:\s*\d+px/.test(q),
  );

/** A mouse or a trackpad. */
export const mouseDevice = () => stubMatchMedia(() => false);
