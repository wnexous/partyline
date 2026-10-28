import * as React from "react";
import { useSyncExternalStore } from "react";

/*
 * What the device is like (UI-24, ENT-01, ACT-01): one module for the three predicates the app
 * asks about a screen, a pointer and a keyboard. Predicates are plain functions of the current
 * environment; the hooks follow it while mounted.
 */

/** Below this width the shell shows the list or the page, never both. */
const MOBILE_BREAKPOINT = 768;

/** A narrow screen: below 768 px wide. */
export function isNarrow(): boolean {
  return typeof window !== "undefined" && window.innerWidth < MOBILE_BREAKPOINT;
}

export function useIsMobile() {
  // Read on the first render: the shell shows the list or the page, never both, and a guess
  // of "wide" on a phone would flash the wrong screen until the effect below corrected it.
  const [isMobile, setIsMobile] = React.useState<boolean>(() => isNarrow());

  React.useEffect(() => {
    const mql = window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`);
    const onChange = () => {
      setIsMobile(isNarrow());
    };
    mql.addEventListener("change", onChange);
    setIsMobile(isNarrow());
    return () => mql.removeEventListener("change", onChange);
  }, []);

  return isMobile;
}

/** A touch-first pointer: focusing a text field there opens the on-screen keyboard. */
export function isCoarsePointer(): boolean {
  return typeof window.matchMedia === "function" && window.matchMedia("(pointer: coarse)").matches;
}

/**
 * Whether a form may focus its first field on mount (UI-24): yes with a mouse or trackpad, no on
 * a coarse pointer, where the keyboard would cover the form before the user asked for it.
 */
export function useAutoFocus(): boolean {
  const [allowed] = React.useState(() => !isCoarsePointer());
  return allowed;
}

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
