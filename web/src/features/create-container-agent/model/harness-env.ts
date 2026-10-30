/*
 * The limits the create form offers (CA-04). The app and credential table lives in the container
 * entity, where the credentials edit reads the same rules.
 */

/** CPU and memory choices: the form asks for one of these, the server takes their numbers. */
export const CPU_OPTIONS = ["0.5", "1", "2", "4", "8"] as const;
export const MEMORY_OPTIONS = ["512", "1024", "2048", "4096", "8192", "16384"] as const;
export const DEFAULT_CPUS = "1";
export const DEFAULT_MEMORY_MB = "2048";
