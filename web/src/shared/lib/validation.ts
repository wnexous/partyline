import { z } from "zod";
import {
  MaxMemberKeyword,
  MaxMemberKeywords,
  MaxProfileBio,
  MaxRoomInstructions,
  MaxTextLength,
  PasswordMinLength,
  UsernamePattern,
} from "@/shared/api/types.gen";
import type { MessageKey } from "@/shared/i18n";

const usernameRe = new RegExp(UsernamePattern);

/** The organization name limit (ORG-01): code points, like the server counts them. */
export const MaxOrgNameLength = 100;

export const msg = (key: MessageKey) => ({ message: key });

export const usernameSchema = z.string().regex(usernameRe, msg("validation.username"));

export const optionalText = z
  .string()
  .trim()
  .optional()
  .transform((v) => (v ? v : undefined));

export const passwordSchema = z.string().min(PasswordMinLength, msg("validation.passwordMin"));

/** The handle as the server reads it: no leading @, no spaces around it, lower case. */
export const normalizeHandle = (text: string) => text.trim().replace(/^@+/, "").toLowerCase();

/**
 * Message text length as the server counts it (MSG-02: Unicode code points, Go's rune count),
 * not UTF-16 units: an emoji is one character.
 */
export function textLength(text: string): number {
  return [...text].length;
}

/** Placeholder values of the schema message keys, for rendering zod issues with useT(). */
export const validationVars = {
  "validation.passwordMin": { min: PasswordMinLength },
  "validation.tooLong": { max: MaxTextLength },
  "validation.keywords": { max: MaxMemberKeywords },
  "validation.keyword": { max: MaxMemberKeyword },
  "validation.instructions": { max: MaxRoomInstructions },
  "validation.bio": { max: MaxProfileBio },
  "validation.orgName": { max: MaxOrgNameLength },
} as const;
