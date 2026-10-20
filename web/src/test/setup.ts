import "@testing-library/jest-dom/vitest";
import { cleanup, configure } from "@testing-library/react";
import { afterEach } from "vitest";

// The suite often runs on a loaded host (parallel workers, other builds): a route that renders in
// 50 ms alone can take over a second there. find* / waitFor poll up to 5 s instead of the 1 s
// default; assertions are unchanged, they only get time to settle.
configure({ asyncUtilTimeout: 5_000 });

afterEach(() => {
  cleanup();
});
