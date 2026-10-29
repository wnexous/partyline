import { z } from "zod";
import {
  MaxSecretValueBytes,
  secretNameRe,
  secretValueLength,
  secretValueTooLong,
} from "@/entities/secret";
import { msg } from "@/shared/lib/validation";

/**
 * A named secret (CA-03): a name the server accepts as an environment variable and a value of 1 to
 * 16384 bytes, counted in UTF-8 bytes like the server counts it.
 */
export const secretSchema = z.object({
  name: z
    .string()
    .trim()
    .refine((v) => secretNameRe.test(v), msg("validation.secretName")),
  value: z
    .string()
    .min(1, msg("validation.required"))
    .refine((v) => !secretValueTooLong(v), msg("validation.secretValue")),
});

/** How full the value input is, for the hint: bytes written of the limit. */
export const secretValueCount = (value: string) =>
  `${secretValueLength(value)}/${MaxSecretValueBytes}`;

export type SecretForm = z.input<typeof secretSchema>;
