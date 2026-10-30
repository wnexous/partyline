import { z } from "zod";
import { msg } from "@/shared/lib/validation";

/*
 * The environment rows of a container AI (CA-04), one mapping per secret: the variable the app reads
 * and the owner's secret that fills it. The rules mirror `spec.EnvNamePattern` and the server's
 * validation; the credential requirement needs the app, so it lives in each form's schema.
 */

/** Mirror of `spec.MaxEnvVars`: the most rows the server accepts. */
export const MAX_ENV_VARS = 64;

/** Mirror of `spec.EnvNamePattern` (`internal/runners/spec`), which the server validates. */
const envNameRe = /^[A-Z_][A-Z0-9_]{0,63}$/;

/** One environment variable of the container, mapped to one of the owner's secrets. */
export const containerEnvRowSchema = z.object({
  var_name: z
    .string()
    .trim()
    .refine((v) => envNameRe.test(v), msg("validation.envName")),
  secret_name: z.string().min(1, msg("validation.required")),
});

/** The shape both the create and the credentials forms share. */
export type ContainerEnvValues = { env: { var_name: string; secret_name: string }[] };

/**
 * The form path of a field the server refused (422): the environment array is the one place the two
 * names differ (`env[0].var` on the wire, `env.0.var_name` in the form).
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
