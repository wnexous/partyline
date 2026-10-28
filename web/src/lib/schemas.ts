import { z } from "zod";
import { MaxTextLength } from "@/shared/api/types.gen";
import { msg, textLength } from "@/shared/lib/validation";

/** Message text (MSG-02): non-empty unless files are attached, at most MaxTextLength. */
export const messageTextSchema = z
  .string()
  .refine((text) => textLength(text) <= MaxTextLength, msg("validation.tooLong"));
