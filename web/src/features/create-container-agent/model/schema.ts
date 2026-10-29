import { z } from "zod";
import { HandlePattern, MaxProfileDisplayName } from "@/shared/api/types.gen";
import { msg, normalizeHandle, textLength } from "@/shared/lib/validation";
import { CONTAINER_HARNESSES, DEFAULT_CPUS, DEFAULT_MEMORY_MB } from "./harness-env";

const handleRe = new RegExp(HandlePattern);
/** Mirror of `spec.EnvNamePattern` (`internal/runners/spec`), which the server validates. */
const envNameRe = /^[A-Z_][A-Z0-9_]{0,63}$/;
/** Mirror of what `spec.ValidRepoURL` accepts: https, ssh/scp or a git@host:path form. */
const repoUrlRe =
  /^(https:\/\/[A-Za-z0-9][A-Za-z0-9.-]*(:[0-9]{1,5})?(\/[A-Za-z0-9._~%+-]+)+\/?|ssh:\/\/([A-Za-z0-9._-]+@)?[A-Za-z0-9][A-Za-z0-9.-]*(:[0-9]{1,5})?(\/[A-Za-z0-9._~%+-]+)+\/?|[A-Za-z0-9._-]+@[A-Za-z0-9][A-Za-z0-9.-]*:\/?[A-Za-z0-9._~%+-]+(\/[A-Za-z0-9._~%+-]+)*)$/;
/** Mirror of `spec.ValidRef`: a git refname the runner accepts. */
const repoRefRe = /^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$/;
const MAX_ENV_VARS = 64;

/** One environment variable of the container, mapped to one of the owner's secrets. */
export const containerEnvSchema = z.object({
  var_name: z
    .string()
    .trim()
    .refine((v) => envNameRe.test(v), msg("validation.envName")),
  secret_name: z.string().min(1, msg("validation.required")),
});

/**
 * POST /container-agents (CA-04): the server, the app, the profile of the new AI, an optional
 * repository and the credentials to expose. Limits and lengths are the server's, counted the way
 * it counts them (code points for the name, the patterns of `internal/runners/spec` for the rest).
 */
export const createContainerAgentSchema = z
  .object({
    runner_id: z.string().min(1, msg("validation.required")),
    harness: z.enum(CONTAINER_HARNESSES),
    display_name: z
      .string()
      .trim()
      .min(1, msg("validation.required"))
      .refine((v) => textLength(v) <= MaxProfileDisplayName, msg("validation.displayName")),
    handle: z
      .string()
      .transform(normalizeHandle)
      .refine((v) => handleRe.test(v), msg("validation.handle")),
    repo_url: z.string().trim(),
    repo_ref: z.string().trim(),
    env: z.array(containerEnvSchema).max(MAX_ENV_VARS),
    cpus: z.string().refine((v) => Number(v) >= 0.1 && Number(v) <= 64, msg("validation.cpus")),
    memory_mb: z
      .string()
      .refine((v) => Number(v) >= 64 && Number(v) <= 65536, msg("validation.memory")),
  })
  .superRefine((value, ctx) => {
    if (value.repo_url !== "" && !repoUrlRe.test(value.repo_url)) {
      ctx.addIssue({
        code: "custom",
        path: ["repo_url"],
        message: "validation.repoUrl",
      });
    }
    if (value.repo_ref !== "" && !repoRefRe.test(value.repo_ref)) {
      ctx.addIssue({
        code: "custom",
        path: ["repo_ref"],
        message: "validation.repoRef",
      });
    }
    // The server refuses a branch without a repository: say it on the URL it needs.
    if (value.repo_url === "" && value.repo_ref !== "") {
      ctx.addIssue({
        code: "custom",
        path: ["repo_url"],
        message: "validation.repoNeedsUrl",
      });
    }
  });

export type CreateContainerAgentForm = z.input<typeof createContainerAgentSchema>;

/** The form's first values: the first connected server and the default limits. */
export function createContainerAgentDefaults(runnerId: string) {
  return {
    runner_id: runnerId,
    harness: "claude",
    display_name: "",
    handle: "",
    repo_url: "",
    repo_ref: "",
    env: [],
    cpus: DEFAULT_CPUS,
    memory_mb: DEFAULT_MEMORY_MB,
  } satisfies CreateContainerAgentForm;
}

/**
 * The form path of a field the server refused (422): the environment array is the one place the
 * two names differ (`env[0].var` on the wire, `env.0.var_name` in the form).
 */
export function formPathOf(field: string): string | null {
  const env = /^env\[(\d+)\]\.(var|secret_name)$/.exec(field);
  if (env) return `env.${env[1]}.${env[2] === "var" ? "var_name" : "secret_name"}`;
  const known = new Set([
    "runner_id",
    "harness",
    "display_name",
    "handle",
    "repo_url",
    "repo_ref",
    "cpus",
    "memory_mb",
  ]);
  return known.has(field) ? field : null;
}
