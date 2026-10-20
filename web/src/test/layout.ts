import { afterEach, beforeEach } from "vitest";

/**
 * happy-dom has no layout: every box measures 0, so virtualized lists would render almost
 * nothing. Gives scroll containers a viewport and rows a height for the duration of a suite.
 */
export function stubLayout({ viewport = 800, row = 60 } = {}) {
  const saved = {
    clientHeight: Object.getOwnPropertyDescriptor(HTMLElement.prototype, "clientHeight"),
    offsetHeight: Object.getOwnPropertyDescriptor(HTMLElement.prototype, "offsetHeight"),
  };
  beforeEach(() => {
    Object.defineProperty(HTMLElement.prototype, "clientHeight", {
      configurable: true,
      get: () => viewport,
    });
    Object.defineProperty(HTMLElement.prototype, "offsetHeight", {
      configurable: true,
      get: () => row,
    });
  });
  afterEach(() => {
    for (const [key, desc] of Object.entries(saved)) {
      if (desc) Object.defineProperty(HTMLElement.prototype, key, desc);
      else delete (HTMLElement.prototype as unknown as Record<string, unknown>)[key];
    }
  });
}
