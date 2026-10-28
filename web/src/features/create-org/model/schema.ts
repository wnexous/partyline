import { z } from "zod";
import { isOrgSlug, MaxOrgNameLength, normalizeSlug } from "@/entities/organization";
import { msg, textLength } from "@/shared/lib/validation";

/** POST /orgs (ORG-01): a name of 1 to 100 characters, counted in code points like the server. */
export const createOrgSchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, msg("validation.required"))
    .refine((v) => textLength(v) <= MaxOrgNameLength, msg("validation.orgName")),
});

/** PATCH /orgs/{org} (OMEM-04): the name as for a new organization, the address as the server reads it. */
export const updateOrgSchema = z.object({
  name: createOrgSchema.shape.name,
  slug: z.string().transform(normalizeSlug).refine(isOrgSlug, msg("validation.orgSlug")),
});

export type CreateOrgForm = z.input<typeof createOrgSchema>;
export type UpdateOrgForm = z.input<typeof updateOrgSchema>;
