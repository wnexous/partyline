import { z } from "zod";
import {
  CONTAINER_HARNESSES,
  containerEnvRowSchema,
  hasCredential,
  MAX_ENV_VARS,
} from "@/entities/container-agent";
import { HandlePattern, MaxProfileDisplayName } from "@/shared/api/types.gen";
import { msg, normalizeHandle, textLength } from "@/shared/lib/validation";
import { DEFAULT_CPUS, DEFAULT_MEMORY_MB } from "./harness-env";

const handleRe = new RegExp(HandlePattern);
/** Mirror of what `spec.ValidRepoURL` accepts: https, ssh/scp or a git@host:path form. */
const repoUrlRe =
  /^(https:\/\/[A-Za-z0-9][A-Za-z0-9.-]*(:[0-9]{1,5})?(\/[A-Za-z0-9._~%+-]+)+\/?|ssh:\/\/([A-Za-z0-9._-]+@)?[A-Za-z0-9][A-Za-z0-9.-]*(:[0-9]{1,5})?(\/[A-Za-z0-9._~%+-]+)+\/?|[A-Za-z0-9._-]+@[A-Za-z0-9][A-Za-z0-9.-]*:\/?[A-Za-z0-9._~%+-]+(\/[A-Za-z0-9._~%+-]+)*)$/;
/** Mirror of `spec.ValidRef`: a git refname the runner accepts. */
const repoRefRe = /^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$/;

/**
 * POST /container-agents (CA-04): the server, the app, the profile of the new AI, an optional
 * repository and the credentials to expose. Limits and lengths are the server's, counted the way
 * it counts them (code points for the name, the patterns of `internal/runners/spec` for the rest).
 * At least one row must map a variable the chosen app reads: the server answers
 * `credentials_required` otherwise, because the app could never sign in.
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
    env: z.array(containerEnvRowSchema).max(MAX_ENV_VARS),
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
    if (!hasCredential(value.harness, value.env)) {
      ctx.addIssue({
        code: "custom",
        path: ["env"],
        message: "validation.credentials",
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
