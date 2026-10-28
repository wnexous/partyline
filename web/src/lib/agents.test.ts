import { describe, expect, it } from "vitest";

import { harnessLabel } from "./agents";

describe("harnessLabel", () => {
  it("names the harness ids the server records", () => {
    expect(harnessLabel("antigravity")).toBe("Antigravity");
    expect(harnessLabel("gemini")).toBe("Gemini");
  });

  it("gives unknown harnesses no label", () => {
    expect(harnessLabel("agent")).toBeUndefined();
  });
});
