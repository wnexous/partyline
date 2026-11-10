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

/**
 * The organization an AI belongs to among the memberships of its profile: the one where deleting it
 * deletes it for good (POS-06). None for a user, or when the viewer does not share it.
 */
export function homeMembershipOf<M extends { delete_effect: string }>(
  memberships: readonly M[],
): M | undefined {
  return memberships.find((m) => m.delete_effect === DeleteEffectDelete);
}
