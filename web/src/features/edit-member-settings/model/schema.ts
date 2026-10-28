import { z } from "zod";
import {
  DeliveryModeAll,
  DeliveryModeMentions,
  DeliveryModeTrusted,
  MaxMemberDisplayName,
  MaxMemberKeyword,
  MaxMemberKeywords,
} from "@/shared/api/types.gen";
import type { MessageKey } from "@/shared/i18n";
import { msg, textLength } from "@/shared/lib/validation";

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
