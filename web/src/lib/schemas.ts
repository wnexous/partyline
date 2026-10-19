import { z } from "zod";
import { MaxTextLength, PasswordMinLength, UsernamePattern } from "./api/types.gen";
import type { MessageKey } from "./i18n";

/*
 * Form schemas built from the generated contract constants (AD-003), so the web app validates
 * with exactly the API's rules. Error messages are i18n keys; forms render them with useT().
 */

const usernameRe = new RegExp(UsernamePattern);

const msg = (key: MessageKey) => ({ message: key });

export const usernameSchema = z.string().regex(usernameRe, msg("validation.username"));

export const passwordSchema = z.string().min(PasswordMinLength, msg("validation.passwordMin"));

const optionalDisplayName = z
  .string()
  .trim()
  .optional()
  .transform((v) => (v ? v : undefined));

/** POST /setup (UI-01). */
export const setupSchema = z.object({
  token: z.string().trim().min(1, msg("validation.required")),
  username: usernameSchema,
  password: passwordSchema,
  display_name: optionalDisplayName,
});

/** POST /auth/login: only presence is checked; the server decides validity (UI-02). */
export const loginSchema = z.object({
  username: z.string().trim().min(1, msg("validation.required")),
  password: z.string().min(1, msg("validation.required")),
});

/** POST /invites/{token}/accept, same rules as the API (UI-03). */
export const acceptInviteSchema = z.object({
  username: usernameSchema,
  password: passwordSchema,
  display_name: optionalDisplayName,
});

/** Message text (MSG-02): non-empty unless files are attached, at most MaxTextLength. */
export const messageTextSchema = z.string().max(MaxTextLength, msg("validation.tooLong"));

export type SetupForm = z.input<typeof setupSchema>;
export type LoginForm = z.input<typeof loginSchema>;
export type AcceptInviteForm = z.input<typeof acceptInviteSchema>;

/** Placeholder values of the schema message keys, for rendering zod issues with useT(). */
export const validationVars = {
  "validation.passwordMin": { min: PasswordMinLength },
  "validation.tooLong": { max: MaxTextLength },
} as const;
