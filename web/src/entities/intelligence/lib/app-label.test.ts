import { describe, expect, it } from "vitest";
import { appLabelOf } from "./app-label";

describe("appLabelOf", () => {
  it("prefers the label the server sends", () => {
    expect(appLabelOf({ app: "claude", apps: [{ label: "Claude Code 2" }] })).toBe("Claude Code 2");
  });

  it("then the readable name of the harness", () => {
    expect(appLabelOf({ app: "claude" })).toBe("Claude Code");
    expect(appLabelOf({ app: "opencode", apps: [{ label: "" }] })).toBe("OpenCode");
    expect(appLabelOf({ app: "pi", apps: [] })).toBe("pi");
  });

  it("then the raw id of a harness it does not know", () => {
    expect(appLabelOf({ app: "agent" })).toBe("agent");
  });
});
