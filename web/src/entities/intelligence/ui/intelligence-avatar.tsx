import type { IntelligenceRef } from "@/shared/api/types.gen";
import { Face } from "@/shared/ui/atoms/face";

/*
 * An intelligence's face (PROF-08): its uploaded image, or initials on a soft tint picked from the
 * design tokens by a hash of its id. Only the id drives the tint, never the kind: people and AIs
 * are drawn from the same palette and look alike, and a rename keeps the face. Red is for errors
 * and green for presence, so neither is a tint.
 */

export interface IntelligenceAvatarProps {
  intelligence: IntelligenceRef;
  /** `xl` is the profile page's: 96 px. */
  size?: "default" | "sm" | "lg" | "xl";
  className?: string;
  /** Hidden from assistive technology: the name sits right next to the face (rows, headers). */
  decorative?: boolean;
  /** A person or an AI is a circle; a group's face is a rounded square, so a mixed list reads at a glance. */
  shape?: "circle" | "square";
}

// Re-exported here so existing importers keep working; the canonical home is shared/lib/avatar.
export { avatarTone, initials } from "@/shared/lib/avatar";

export function IntelligenceAvatar({
  intelligence,
  size = "default",
  className,
  decorative = false,
  shape = "circle",
}: IntelligenceAvatarProps) {
  return (
    <Face
      id={intelligence.id}
      name={intelligence.display_name.trim() || intelligence.handle || "?"}
      imageUrl={intelligence.avatar_url}
      size={size}
      shape={shape}
      decorative={decorative}
      className={className}
      // The hairline, in the tint's own ink, keeps a pale face visible on a pale surface.
      fallbackClassName="ring-1 ring-current/25 ring-inset"
      testId="intelligence-avatar"
    />
  );
}
