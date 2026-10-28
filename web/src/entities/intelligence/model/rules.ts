import { IntelligenceRolePerson } from "@/shared/api/types.gen";

/**
 * Whether the viewer may open a direct message with a target (PDM-04, DM-05): never with
 * themselves, nor with a person known only through a chat platform.
 */
export function canMessage(viewerId: string, target: { id: string; role: string }): boolean {
  return target.id !== viewerId && target.role !== IntelligenceRolePerson;
}
