import type { MessageKey } from "@/shared/i18n";

/*
 * What a container AI needs to know about its app (CA-04): the four harnesses with a live channel
 * (AD-035) and the environment variables each one reads its provider key from, taken from the agent
 * image's table (deploy/agent/README.md). The table is the same one the server validates against: at
 * least one of the variables must be mapped, or the app inside the container can never sign in.
 */

/** The harnesses a container can run, in the order the forms list them. */
export const CONTAINER_HARNESSES = ["claude", "opencode", "pi", "omp"] as const;
export type ContainerHarness = (typeof CONTAINER_HARNESSES)[number];

/** Every variable each harness reads its provider key from (any one of them is enough). */
export const CREDENTIAL_VARS = {
  claude: ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"],
  opencode: [
    "OPENCODE_API_KEY",
    "ANTHROPIC_API_KEY",
    "OPENAI_API_KEY",
    "GEMINI_API_KEY",
    "OPENROUTER_API_KEY",
  ],
  pi: [
    "OPENCODE_API_KEY",
    "ANTHROPIC_API_KEY",
    "OPENAI_API_KEY",
    "GEMINI_API_KEY",
    "OPENROUTER_API_KEY",
  ],
  omp: [
    "OPENCODE_API_KEY",
    "ANTHROPIC_API_KEY",
    "OPENAI_API_KEY",
    "GEMINI_API_KEY",
    "OPENROUTER_API_KEY",
  ],
} as const satisfies Record<ContainerHarness, readonly string[]>;

/** The first credential variable the agent image documents for each harness (what a new row gets). */
export const CREDENTIAL_SUGGESTION = {
  claude: "CLAUDE_CODE_OAUTH_TOKEN",
  opencode: "OPENCODE_API_KEY",
  pi: "OPENCODE_API_KEY",
  omp: "OPENCODE_API_KEY",
} as const satisfies Record<ContainerHarness, string>;

/** The table of variables that harness reads, as a hint under the rows. */
export const CREDENTIAL_HINT = {
  claude: "containerAgent.credsClaude",
  opencode: "containerAgent.credsOthers",
  pi: "containerAgent.credsOthers",
  omp: "containerAgent.credsOthers",
} as const satisfies Record<ContainerHarness, MessageKey>;

/** Whether `name` is a variable the app reads its provider key from. */
export function isCredentialVar(harness: string, name: string): boolean {
  return (CREDENTIAL_VARS[harness as ContainerHarness] ?? []).some((v) => v === name);
}

/** A row of the environment form: the variable and the secret that fills it. */
export type ContainerEnvRow = { var_name?: string; secret_name?: string };

/**
 * Whether the form maps at least one credential the app reads to a secret. The server refuses a
 * create or an edit without one (`credentials_required`), so both forms refuse to submit it.
 */
export function hasCredential(
  harness: string,
  rows: readonly ContainerEnvRow[] | undefined,
): boolean {
  return (rows ?? []).some(
    (row) => Boolean(row.secret_name) && isCredentialVar(harness, row.var_name ?? ""),
  );
}
