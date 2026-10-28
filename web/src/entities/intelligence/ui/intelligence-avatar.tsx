import { Avatar, AvatarFallback, AvatarImage } from "@/shared/ui/primitives/avatar";
import type { IntelligenceRef } from "@/shared/api/types.gen";
import { avatarTone, initials } from "@/shared/lib/avatar";
import { cn } from "@/shared/lib/utils";

/*
 * An intelligence's face (PROF-08): its uploaded image, or initials on a soft tint picked from the
 * design tokens by a hash of its id. Only the id drives the tint, never the kind: people and AIs
 * are drawn from the same palette and look alike, and a rename keeps the face. Red is for errors
 * and green for presence, so neither is a tint.
 */

const PIXELS = { sm: 24, default: 32, lg: 40, xl: 96 } as const;

export interface IntelligenceAvatarProps {
  intelligence: IntelligenceRef;
  /** `xl` is the profile page's: 96 px. */
  size?: "default" | "sm" | "lg" | "xl";
  className?: string;
  /** Hidden from assistive technology: the name sits right next to the face (rows, headers). */
  decorative?: boolean;
}

// Re-exported here so existing importers keep working; the canonical home is shared/lib/avatar.
export { avatarTone, initials } from "@/shared/lib/avatar";

export function IntelligenceAvatar({
  intelligence,
  size = "default",
  className,
  decorative = false,
}: IntelligenceAvatarProps) {
  const name = intelligence.display_name.trim() || intelligence.handle || "?";
  return (
    <Avatar
      size={size === "xl" ? "lg" : size}
      data-testid="intelligence-avatar"
      aria-hidden={decorative || undefined}
      className={cn("shrink-0", size === "xl" && "data-[size=lg]:size-24", className)}
    >
      {intelligence.avatar_url ? (
        <AvatarImage
          src={intelligence.avatar_url}
          alt={decorative ? "" : name}
          width={PIXELS[size]}
          height={PIXELS[size]}
        />
      ) : null}
      <AvatarFallback
        className={cn(
          // The hairline, in the tint's own ink, keeps a pale face visible on a pale surface.
          "text-xs font-semibold tracking-wide uppercase ring-1 ring-current/25 ring-inset select-none",
          size === "xl" && "text-2xl",
          avatarTone(intelligence.id),
        )}
      >
        {initials(name)}
      </AvatarFallback>
    </Avatar>
  );
}
