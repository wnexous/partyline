import { z } from "zod";
import { msg } from "@/shared/lib/validation";

/** POST /auth/login: only presence is checked; the server decides validity (UI-02). */
export const loginSchema = z.object({
  username: z.string().trim().min(1, msg("validation.required")),
  password: z.string().min(1, msg("validation.required")),
});

export type LoginForm = z.input<typeof loginSchema>;
