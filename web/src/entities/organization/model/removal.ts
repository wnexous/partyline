import { DeleteEffectDelete } from "@/shared/api/types.gen";

/**
 * How the viewer may take a member out of an organization: `delete` (the identity goes, POS-06) or
 * `remove` (it leaves the organization only, POS-05). Null when the viewer may do neither.
 */
export function removalOf(member: {
  can_delete: boolean;
  delete_effect: string;
}): "delete" | "remove" | null {
  if (!member.can_delete) return null;
  return member.delete_effect === DeleteEffectDelete ? "delete" : "remove";
}
