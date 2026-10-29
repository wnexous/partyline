import type { MessageKey } from "@/shared/i18n";

/*
 * What a container AI needs to know about its app (CA-04): the four harnesses with a live channel
 * (AD-035) and the environment variable each one reads its provider key from, taken from the agent
 * image's table (deploy/agent/README.md). The suggestion is a convenience: any variable of the
 * table works, and the owner maps it to a stored secret.
 */

/** The harnesses a container can run, in the order the form lists them. */
export const CONTAINER_HARNESSES = ["claude", "opencode", "pi", "omp"] as const;
export type ContainerHarness = (typeof CONTAINER_HARNESSES)[number];

/** The first credential variable the agent image documents for each harness. */
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

/** CPU and memory choices: the form asks for one of these, the server takes their numbers. */
export const CPU_OPTIONS = ["0.5", "1", "2", "4", "8"] as const;
export const MEMORY_OPTIONS = ["512", "1024", "2048", "4096", "8192", "16384"] as const;
export const DEFAULT_CPUS = "1";
export const DEFAULT_MEMORY_MB = "2048";
