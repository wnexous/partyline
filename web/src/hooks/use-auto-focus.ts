import { useState } from "react";

/** A touch-first device: focusing a text field there opens the on-screen keyboard. */
export function isCoarsePointer(): boolean {
  return typeof window.matchMedia === "function" && window.matchMedia("(pointer: coarse)").matches;
}

/**
 * Whether a form may focus its first field on mount (UI-24): yes with a mouse or trackpad, no on
 * a coarse pointer, where the keyboard would cover the form before the user asked for it.
 */
export function useAutoFocus(): boolean {
  const [allowed] = useState(() => !isCoarsePointer());
  return allowed;
}
