import { z } from "zod";
import { msg, optionalText, passwordSchema, usernameSchema } from "@/shared/lib/validation";

/** POST /setup (UI-01). */
export const setupSchema = z.object({
  token: z.string().trim().min(1, msg("validation.required")),
  username: usernameSchema,
  password: passwordSchema,
  display_name: optionalText,
});

export type SetupForm = z.input<typeof setupSchema>;
