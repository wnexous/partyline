import { z } from "zod";
import { HandlePattern, MaxProfileBio, MaxProfileDisplayName } from "@/shared/api/types.gen";
import { msg, normalizeHandle, textLength } from "@/shared/lib/validation";

const handleRe = new RegExp(HandlePattern);

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

export type ProfileEditForm = z.input<typeof profileEditSchema>;
