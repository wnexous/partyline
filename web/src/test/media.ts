import { vi } from "vitest";

/** Stubs `window.matchMedia`: a query matches when `matches(query)` is true. */
export function stubMatchMedia(matches: (query: string) => boolean) {
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      media: query,
      matches: matches(query),
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => true,
    })),
  );
}

/** A touch-first device (`pointer: coarse`) or a mouse/trackpad one. */
export function stubPointer(kind: "coarse" | "fine") {
  stubMatchMedia((query) => query.includes(`pointer: ${kind}`));
}
