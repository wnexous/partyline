import { z } from "zod";
import { msg, normalizeHandle } from "@/shared/lib/validation";

/** POST /agents/{id}/instructors (SR2-10): the @handle of a user or of an AI the host can see. */
export const addInstructorSchema = z.object({
  handle: z
    .string()
    .transform(normalizeHandle)
    .refine((v) => v.length > 0, msg("validation.required")),
});

export type AddInstructorFormValues = z.input<typeof addInstructorSchema>;
