import { z } from "zod";
import {
  DeliveryModeAll,
  DeliveryModeMentions,
  DeliveryModeTrusted,
  HandlePattern,
  MaxMemberDisplayName,
  MaxMemberKeyword,
  MaxMemberKeywords,
  MaxProfileBio,
  MaxProfileDisplayName,
  MaxRoomInstructions,
  MaxTextLength,
  PasswordMinLength,
  UsernamePattern,
} from "./api/types.gen";
import type { MessageKey } from "./i18n";

/*
 * Form schemas built from the generated contract constants (AD-003), so the web app validates
 * with exactly the API's rules. Error messages are i18n keys; forms render them with useT().
 */

const usernameRe = new RegExp(UsernamePattern);

const msg = (key: MessageKey) => ({ message: key });

export const usernameSchema = z.string().regex(usernameRe, msg("validation.username"));

export const passwordSchema = z.string().min(PasswordMinLength, msg("validation.passwordMin"));

const optionalText = z
  .string()
  .trim()
  .optional()
  .transform((v) => (v ? v : undefined));

/** POST /setup (UI-01). */
export const setupSchema = z.object({
  token: z.string().trim().min(1, msg("validation.required")),
  username: usernameSchema,
  password: passwordSchema,
  display_name: optionalText,
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
  display_name: optionalText,
});

/** POST /groups: a name is required; the members are picked apart from the form (LAY-04). */
export const createGroupSchema = z.object({
  name: z.string().trim().min(1, msg("validation.required")),
});

const handleRe = new RegExp(HandlePattern);

/** The handle as the server reads it: no leading @, no spaces around it, lower case. */
export const normalizeHandle = (text: string) => text.trim().replace(/^@+/, "").toLowerCase();

/**
 * The profile of any intelligence (PROF-03, PROF-05, PROF-11): a display name, a handle and a
 * bio, with the limits of the contract counted in code points like the server counts them.
 */
export const profileEditSchema = z.object({
  display_name: z
    .string()
    .trim()
    .min(1, msg("validation.required"))
    .refine((v) => textLength(v) <= MaxProfileDisplayName, msg("validation.displayName")),
  handle: z
    .string()
    .transform(normalizeHandle)
    .refine((v) => handleRe.test(v), msg("validation.handle")),
  bio: z
    .string()
    .trim()
    .refine((v) => textLength(v) <= MaxProfileBio, msg("validation.bio")),
});

/** PATCH /rooms/{id}: retention in whole days, 0 keeps messages forever (OPS-02). */
export const roomSettingsSchema = z.object({
  name: z.string().trim().min(1, msg("validation.required")),
  description: z.string().trim(),
  retention_days: z
    .number(msg("validation.retention"))
    .int(msg("validation.retention"))
    .min(0, msg("validation.retention")),
  // INS-01: free text for the room's agents, counted in code points like the server.
  instructions: z
    .string()
    .refine((v) => textLength(v) <= MaxRoomInstructions, msg("validation.instructions")),
});

/** POST /tokens (UI-19): expiry is picked from presets and converted at submit. */
export const TOKEN_EXPIRY_DAYS = ["never", "7", "30", "90", "365"] as const;
export const createTokenSchema = z.object({
  kind: z.enum(["agent", "user"]),
  name: z.string().trim().min(1, msg("validation.required")).max(64, msg("validation.tokenName")),
  expiry: z.enum(TOKEN_EXPIRY_DAYS),
});

/** PATCH /me: the display name may be empty (the username shows instead). */
export const profileSchema = z.object({
  display_name: z.string().trim().max(64, msg("validation.displayName")),
});

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

/** DELETE /me: the current password proves it is the person who deletes their own account. */
export const deleteAccountSchema = z.object({
  password: z.string().min(1, msg("validation.required")),
});

/**
 * Message text length as the server counts it (MSG-02: Unicode code points, Go's rune count),
 * not UTF-16 units: an emoji is one character.
 */
export function textLength(text: string): number {
  return [...text].length;
}

/** Message text (MSG-02): non-empty unless files are attached, at most MaxTextLength. */
export const messageTextSchema = z
  .string()
  .refine((text) => textLength(text) <= MaxTextLength, msg("validation.tooLong"));

/**
 * PATCH /rooms/{id}/members/{mid} agent settings (MEM-01..05). Limits count code points like the
 * server; a keyword is checked when it is added (`keywordError`), the list here.
 */
export const memberSettingsSchema = z.object({
  display_name: z
    .string()
    .trim()
    .refine((v) => textLength(v) <= MaxMemberDisplayName, msg("validation.displayName")),
  delivery: z.enum([DeliveryModeAll, DeliveryModeMentions, DeliveryModeTrusted]),
  keywords: z.array(z.string()).max(MaxMemberKeywords, msg("validation.keywords")),
});

/** The i18n key of what is wrong with a keyword about to be added, or null when it is valid. */
export function keywordError(keyword: string): MessageKey | null {
  const n = textLength(keyword.trim());
  return n < 1 || n > MaxMemberKeyword ? "validation.keyword" : null;
}

export type SetupForm = z.input<typeof setupSchema>;
export type LoginForm = z.input<typeof loginSchema>;
export type AcceptInviteForm = z.input<typeof acceptInviteSchema>;
export type CreateGroupForm = z.input<typeof createGroupSchema>;
export type CreateTokenForm = z.input<typeof createTokenSchema>;
export type ProfileForm = z.input<typeof profileSchema>;
export type ProfileEditForm = z.input<typeof profileEditSchema>;
export type ChangePasswordForm = z.input<typeof changePasswordSchema>;
export type DeleteAccountForm = z.input<typeof deleteAccountSchema>;
export type RoomSettingsForm = z.input<typeof roomSettingsSchema>;
export type MemberSettingsForm = z.input<typeof memberSettingsSchema>;

/** Placeholder values of the schema message keys, for rendering zod issues with useT(). */
export const validationVars = {
  "validation.passwordMin": { min: PasswordMinLength },
  "validation.tooLong": { max: MaxTextLength },
  "validation.keywords": { max: MaxMemberKeywords },
  "validation.keyword": { max: MaxMemberKeyword },
  "validation.instructions": { max: MaxRoomInstructions },
  "validation.bio": { max: MaxProfileBio },
} as const;
