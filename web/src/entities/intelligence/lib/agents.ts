/**
 * Readable names of the harness ids the server records (agents.HarnessLabel). codex, gemini and
 * antigravity are no longer supported (AD-035); their labels stay for AIs created with them.
 */
const HARNESS_LABELS: Record<string, string> = {
  claude: "Claude Code",
  opencode: "OpenCode",
  pi: "pi",
  omp: "oh-my-pi",
  codex: "Codex",
  gemini: "Gemini",
  antigravity: "Antigravity",
};

/** The harness's readable name; unknown harnesses ("agent") get undefined. */
export const harnessLabel = (id: string): string | undefined => HARNESS_LABELS[id];
