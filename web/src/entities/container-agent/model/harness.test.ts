import { describe, expect, it } from "vitest";
import {
  CONTAINER_HARNESSES,
  CREDENTIAL_SUGGESTION,
  hasCredential,
  isCredentialVar,
} from "./harness";

/*
 * The credential table of a container AI (CA-04), the same one the agent image reads and the server
 * validates: each app has its own variables, any one is enough, and a mapping needs a secret.
 */

describe("the credentials of a container AI (CA-04)", () => {
  it("accepts the variables each app reads and refuses the one of another app", () => {
    for (const harness of CONTAINER_HARNESSES) {
      expect(isCredentialVar(harness, CREDENTIAL_SUGGESTION[harness])).toBe(true);
      expect(isCredentialVar(harness, "MODEL_NAME")).toBe(false);
    }
    expect(isCredentialVar("claude", "CLAUDE_CODE_OAUTH_TOKEN")).toBe(true);
    expect(isCredentialVar("claude", "OPENCODE_API_KEY")).toBe(false);
    expect(isCredentialVar("opencode", "OPENROUTER_API_KEY")).toBe(true);
    expect(isCredentialVar("pi", "OPENCODE_API_KEY")).toBe(true);
    expect(isCredentialVar("omp", "ANTHROPIC_API_KEY")).toBe(true);
    expect(isCredentialVar("codex", "OPENCODE_API_KEY")).toBe(false);
  });

  it("needs a row that maps a credential to a secret", () => {
    expect(hasCredential("claude", [])).toBe(false);
    expect(hasCredential("claude", undefined)).toBe(false);
    expect(hasCredential("claude", [{ var_name: "ANTHROPIC_API_KEY", secret_name: "" }])).toBe(
      false,
    );
    expect(hasCredential("claude", [{ var_name: "MODEL_NAME", secret_name: "KEY" }])).toBe(false);
    expect(hasCredential("claude", [{ var_name: "ANTHROPIC_API_KEY", secret_name: "KEY" }])).toBe(
      true,
    );
  });
});
