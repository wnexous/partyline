import { z } from "zod";
import { msg } from "@/shared/lib/validation";

/** DELETE /me: the current password proves it is the person who deletes their own account. */
export const deleteAccountSchema = z.object({
  password: z.string().min(1, msg("validation.required")),
});

export type DeleteAccountForm = z.input<typeof deleteAccountSchema>;
