import { z } from "zod";
import { containerEnvRowSchema, hasCredential, MAX_ENV_VARS } from "@/entities/container-agent";

/**
 * PATCH /agents/{id}/container (CA-04): the environment the container is created again with. The
 * rows are the create form's, and at least one has to map a variable the app reads: without a
 * credential the server refuses the edit just as it refuses a create.
 */
export function containerCredentialsSchema(harness: string) {
  return z
    .object({
      env: z.array(containerEnvRowSchema).max(MAX_ENV_VARS),
    })
    .superRefine((value, ctx) => {
      if (!hasCredential(harness, value.env)) {
        ctx.addIssue({ code: "custom", path: ["env"], message: "validation.credentials" });
      }
    });
}

export type ContainerCredentialsForm = z.input<ReturnType<typeof containerCredentialsSchema>>;
