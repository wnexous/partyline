import { IntelligenceRolePerson } from "@/shared/api/types.gen";

/**
 * Whether the viewer may open a direct message with a target (PDM-04, DM-05): never with
 * themselves, nor with a person known only through a chat platform.
 */
export function canMessage(viewerId: string, target: { id: string; role: string }): boolean {
  return target.id !== viewerId && target.role !== IntelligenceRolePerson;
}

/**
 * Whether the viewer manages the AI of this profile in the AI's home organization: the identities whose
 * messages the AI trusts (POS-10), listed as the profile's `instructed_by`. It is who may give the AI a
 * secret (SR-04, SR-05); a profile that does not show the list (the viewer is outside that organization)
 * says no.
 */
export function managesAI(
  profile: { ai?: { instructed_by?: { id: string }[] } } | undefined,
  viewerId: string,
): boolean {
  return profile?.ai?.instructed_by?.some((ref) => ref.id === viewerId) ?? false;
}
