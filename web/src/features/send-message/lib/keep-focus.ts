import type { SyntheticEvent } from "react";

/** Cancels the press that would move focus off the text box; the click that follows still happens. */
export const keepFocus = (e: SyntheticEvent) => e.preventDefault();
