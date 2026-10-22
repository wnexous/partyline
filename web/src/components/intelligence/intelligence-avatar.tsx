import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import type { IntelligenceRef } from "@/lib/api/types.gen";
import { cn } from "@/lib/utils";

/*
 * An intelligence's face (PROF-08): its uploaded image, or initials on a colour picked from the
 * design tokens by a hash of its id. Only the id drives the colour, so a rename keeps the face.
 */

const TONES = [
  "bg-primary text-primary-foreground",
  "bg-tape text-tape-foreground",
  "bg-secondary text-secondary-foreground",
  "bg-accent text-accent-foreground",
  "bg-success/15 text-success",
  "bg-warning/20 text-foreground",
  "bg-info/15 text-info",
  "bg-destructive/12 text-destructive",
] as const;

/** First letter of the first and of the last word; the single letter for a one-word name. */
export function initials(displayName: string): string {
  const words = displayName.trim().split(/\s+/).filter(Boolean);
  const firstWord = words[0];
  if (!firstWord) return "?";
  const lastWord = words.length > 1 ? (words[words.length - 1] ?? firstWord) : firstWord;
  const first = Array.from(firstWord)[0] ?? "";
  const last = Array.from(lastWord)[0] ?? "";
  return `${first}${last}`.toUpperCase();
}

/** Token background for the initials: stable per id, spread across eight colours. */
export function avatarTone(id: string): string {
  let hash = 0;
  for (let i = 0; i < id.length; i += 1) {
    hash = (hash * 31 + id.charCodeAt(i)) >>> 0;
  }
  return TONES[hash % TONES.length] ?? TONES[0];
}

export interface IntelligenceAvatarProps {
  intelligence: IntelligenceRef;
  size?: "default" | "sm" | "lg";
  className?: string;
}

export function IntelligenceAvatar({
  intelligence,
  size = "default",
  className,
}: IntelligenceAvatarProps) {
  const name = intelligence.display_name.trim() || intelligence.handle || "?";
  return (
    <Avatar size={size} data-testid="intelligence-avatar" className={cn("shrink-0", className)}>
      {intelligence.avatar_url ? (
        <AvatarImage
          src={intelligence.avatar_url}
          alt={name}
          width={size === "sm" ? 24 : size === "lg" ? 40 : 32}
          height={size === "sm" ? 24 : size === "lg" ? 40 : 32}
        />
      ) : null}
      <AvatarFallback
        className={cn(
          "text-xs font-semibold tracking-wide uppercase select-none",
          avatarTone(intelligence.id),
        )}
      >
        {initials(name)}
      </AvatarFallback>
    </Avatar>
  );
}
