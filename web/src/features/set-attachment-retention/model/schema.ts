import { z } from "zod";
import { msg } from "@/shared/lib/validation";

/** The largest attachment retention the server takes (RET-02): about a hundred years. */
export const MAX_ATTACHMENT_RETENTION_DAYS = 36500;

/** PATCH /admin/settings: whole days, 0 keeps attachments forever (RET-01). */
export const attachmentRetentionSchema = z.object({
  attachment_retention_days: z
    .number(msg("validation.attachmentRetention"))
    .int(msg("validation.attachmentRetention"))
    .min(0, msg("validation.attachmentRetention"))
    .max(MAX_ATTACHMENT_RETENTION_DAYS, msg("validation.attachmentRetention")),
});

export type AttachmentRetentionForm = z.input<typeof attachmentRetentionSchema>;
