// vitest-axe 0.1 augments the legacy `Vi` namespace; expose its matcher on vitest's Assertion.
import "vitest";
import type { AxeMatchers } from "vitest-axe/matchers";

declare module "vitest" {
  // biome-ignore lint/suspicious/noExplicitAny: mirrors vitest's own Assertion signature
  interface Assertion<T = any> extends AxeMatchers {}
  interface AsymmetricMatchersContaining extends AxeMatchers {}
}
