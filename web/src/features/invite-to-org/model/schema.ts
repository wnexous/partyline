import { z } from "zod";
import { msg, normalizeHandle } from "@/shared/lib/validation";

/** POST /orgs/{org}/invitations (OMEM-01): whom, with what permission and in which position (none: empty). */
export const inviteSchema = z.object({
  handle: z
    .string()
    .transform(normalizeHandle)
    .refine((v) => v.length > 0, msg("validation.required")),
  permission: z.enum(["admin", "member", "guest"]),
  position_id: z.string(),
});

export type InviteFormValues = z.input<typeof inviteSchema>;
