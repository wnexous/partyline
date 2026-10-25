import { useSyncExternalStore } from "react";

/*
 * Whether Enter sends the message (ENT-01, ENT-02). With a mouse or a trackpad it does, and
 * Shift+Enter adds a line. On a touch device the on-screen Return key is for writing, so it adds
 * a line and only the Send button sends.
 */

/** The two ways a device says it is touch-first: its main pointer is coarse, or it cannot hover. */
const TOUCH_QUERIES = ["(pointer: coarse)", "(hover: none)"] as const;

const canMatch = () => typeof window !== "undefined" && typeof window.matchMedia === "function";

/**
 * A touch-first device: a phone or a tablet, or any device Chrome emulates as one. Only the main
 * pointer counts (`pointer`, `hover`), not `any-pointer`: a laptop with a touch screen and a
 * trackpad is driven with the trackpad, and Enter sends there.
 */
export function isTouchFirst(): boolean {
  return canMatch() && TOUCH_QUERIES.some((query) => window.matchMedia(query).matches);
}

/** True when Enter sends the message, false when it adds a new line. */
export const enterSends = () => !isTouchFirst();

function subscribe(onChange: () => void) {
  if (!canMatch()) return () => {};
  const lists = TOUCH_QUERIES.map((query) => window.matchMedia(query));
  for (const list of lists) list.addEventListener("change", onChange);
  return () => {
    for (const list of lists) list.removeEventListener("change", onChange);
  };
}

/** `enterSends()` that follows the device when its main pointer changes (a convertible's modes). */
export function useEnterSends(): boolean {
  return useSyncExternalStore(subscribe, enterSends, () => true);
}

/**
 * `isTouchFirst()` that follows the device when its main pointer changes. A touch-first device
 * opens the actions of a row with a long press and a sheet; a mouse with a right click, a "⋯" button
 * and a menu (ACT-01, ACT-02).
 */
export const useTouchFirst = (): boolean => !useEnterSends();
