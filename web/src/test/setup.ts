import "@testing-library/jest-dom/vitest";
import { cleanup, configure } from "@testing-library/react";
import { afterEach } from "vitest";

// findBy*/waitFor default to 1 s, which a loaded machine (the Go suite running in parallel)
// can exceed; they still resolve as soon as the element appears.
configure({ asyncUtilTimeout: 10_000 });

afterEach(() => {
  cleanup();
});
