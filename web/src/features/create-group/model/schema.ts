import { z } from "zod";
import { msg } from "@/shared/lib/validation";

/** POST /groups: a name is required; the members are picked apart from the form (LAY-04). */
export const createGroupSchema = z.object({
  name: z.string().trim().min(1, msg("validation.required")),
});

export type CreateGroupForm = z.input<typeof createGroupSchema>;
