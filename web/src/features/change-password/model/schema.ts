import { z } from "zod";
import { msg, passwordSchema } from "@/shared/lib/validation";

/** POST /me/password; the repeat field never leaves the browser. */
export const changePasswordSchema = z
  .object({
    current_password: z.string().min(1, msg("validation.required")),
    new_password: passwordSchema,
    repeat_password: z.string(),
  })
  .refine((v) => v.new_password === v.repeat_password, {
    path: ["repeat_password"],
    ...msg("validation.passwordMatch"),
  });

export type ChangePasswordForm = z.input<typeof changePasswordSchema>;
