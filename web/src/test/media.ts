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

/**
 * A viewport `width` px wide: `window.innerWidth` and the `(max-width: Npx)` media queries
 * agree with it, which is all the app's mobile switch reads (hooks/use-mobile.ts).
 */
export function stubViewport(width: number) {
  vi.stubGlobal("innerWidth", width);
  stubMatchMedia((query) => {
    const max = /max-width:\s*(\d+)px/.exec(query);
    return max ? width <= Number(max[1]) : false;
  });
}
