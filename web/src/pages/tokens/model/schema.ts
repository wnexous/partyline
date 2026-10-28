import { z } from "zod";
import { msg } from "@/shared/lib/validation";

/** POST /tokens (UI-19): expiry is picked from presets and converted at submit. */
export const TOKEN_EXPIRY_DAYS = ["never", "7", "30", "90", "365"] as const;
export const createTokenSchema = z.object({
  kind: z.enum(["agent", "user"]),
  name: z.string().trim().min(1, msg("validation.required")).max(64, msg("validation.tokenName")),
  expiry: z.enum(TOKEN_EXPIRY_DAYS),
});

export type CreateTokenForm = z.input<typeof createTokenSchema>;
