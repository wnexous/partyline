import type { IntelligenceProfile, IntelligenceRef } from "@/shared/api/types.gen";

/** The summary every list shows, taken from a full profile. */
export function refOfProfile(p: IntelligenceProfile): IntelligenceRef {
  return {
    id: p.id,
    kind: p.kind,
    role: p.role,
    display_name: p.display_name,
    handle: p.handle,
    avatar_url: p.avatar_url,
    platform: p.platform,
  };
}
