import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import type { IntelligenceRef } from "@/lib/api/types.gen";
import { cn } from "@/lib/utils";

/*
 * An intelligence's face (PROF-08): its uploaded image, or initials on a grey picked from the
 * design tokens by a hash of its id. Only the id drives the grey, so a rename keeps the face.
 * Red is reserved for errors, so it is not one of the tones.
 */

const TONES = [
  "bg-primary text-primary-foreground",
  "bg-tape-strong text-background",
  "bg-muted-foreground text-background",
  "bg-input text-foreground",
  "bg-tape text-tape-foreground",
  "bg-accent text-accent-foreground",
  "bg-secondary text-secondary-foreground ring-1 ring-border ring-inset",
  "bg-card text-foreground ring-1 ring-input ring-inset",
] as const;

/** First letter of the first and of the last word; the single letter for a one-word name. */
export function initials(displayName: string): string {
  const words = displayName.trim().split(/\s+/).filter(Boolean);
  const firstWord = words[0];
  if (!firstWord) return "?";
  const lastWord = words.length > 1 ? words[words.length - 1] : undefined;
  const first = Array.from(firstWord)[0] ?? "";
  const last = lastWord ? (Array.from(lastWord)[0] ?? "") : "";
  return `${first}${last}`.toUpperCase();
}

/** Token background for the initials: stable per id, spread across eight greys. */
export function avatarTone(id: string): string {
  let hash = 0;
  for (let i = 0; i < id.length; i += 1) {
    hash = (hash * 31 + id.charCodeAt(i)) >>> 0;
  }
  return TONES[hash % TONES.length] ?? TONES[0];
}

const PIXELS = { sm: 24, default: 32, lg: 40, xl: 96 } as const;

export interface IntelligenceAvatarProps {
  intelligence: IntelligenceRef;
  /** `xl` is the profile page's: 96 px. */
  size?: "default" | "sm" | "lg" | "xl";
  className?: string;
  /** Hidden from assistive technology: the name sits right next to the face (rows, headers). */
  decorative?: boolean;
}

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
          "text-xs font-semibold tracking-wide uppercase select-none",
          size === "xl" && "text-2xl",
          avatarTone(intelligence.id),
        )}
      >
        {initials(name)}
      </AvatarFallback>
    </Avatar>
  );
}
