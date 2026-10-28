import { z } from "zod";
import { optionalText, passwordSchema, usernameSchema } from "@/shared/lib/validation";

/** POST /invites/{token}/accept, same rules as the API (UI-03). */
export const acceptInviteSchema = z.object({
  username: usernameSchema,
  password: passwordSchema,
  display_name: optionalText,
});

export type AcceptInviteForm = z.input<typeof acceptInviteSchema>;
