import { z } from "zod";
import { MaxRoomInstructions } from "@/shared/api/types.gen";
import { msg, textLength } from "@/shared/lib/validation";

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

export type RoomSettingsForm = z.input<typeof roomSettingsSchema>;
