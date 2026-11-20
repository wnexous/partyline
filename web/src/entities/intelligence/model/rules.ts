import { IntelligenceRolePerson } from "@/shared/api/types.gen";

/**
 * Whether the viewer may open a direct message with a target (PDM-04, DM-05): never with
 * themselves, nor with a person known only through a chat platform.
 */
export function canMessage(viewerId: string, target: { id: string; role: string }): boolean {
  return target.id !== viewerId && target.role !== IntelligenceRolePerson;
}

/**
 * Whether the viewer instructs the AI of this profile: its host (`ai.owner`) or one of the identities the
 * profile lists as its instructors (`instructed_by`), whose messages the AI trusts (SR2-07). It is who may
 * give the AI a secret (SR-04, SR-05); a profile that does not show the list says no for anyone but the host.
 */
export function managesAI(
  profile: { ai?: { owner?: { id: string }; instructed_by?: { id: string }[] } } | undefined,
  viewerId: string,
): boolean {
  if (!profile?.ai) return false;
  if (profile.ai.owner?.id === viewerId) return true;
  return profile.ai.instructed_by?.some((ref) => ref.id === viewerId) ?? false;
}
