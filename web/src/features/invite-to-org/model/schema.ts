import { z } from "zod";
import { msg, normalizeHandle } from "@/shared/lib/validation";

/** POST /orgs/{org}/invitations (OMEM-01, SR2-01): whom, and as an admin or a member. */
export const inviteSchema = z.object({
  handle: z
    .string()
    .transform(normalizeHandle)
    .refine((v) => v.length > 0, msg("validation.required")),
  permission: z.enum(["admin", "member"]),
});

export type InviteFormValues = z.input<typeof inviteSchema>;
