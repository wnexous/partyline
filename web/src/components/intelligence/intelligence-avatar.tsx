import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import type { IntelligenceKind, IntelligenceRef } from "@/lib/api/types.gen";
import { cn } from "@/lib/utils";

/*
 * An intelligence's face (PROF-08): its uploaded image, or initials on a soft tint picked from the
 * design tokens by a hash of its id. The kind chooses the family, so who is talking reads before
 * the name does: warm tints are people's faces (users, people on a chat platform, groups), cool
 * tints are AIs'. The hash only picks the tint inside the family, and only the id drives it, so a
 * rename keeps the face. Red is for errors and green for presence, so neither is a tint.
 */

const PEOPLE_TONES = [
  "bg-tint-apricot text-tint-apricot-foreground",
  "bg-tint-ochre text-tint-ochre-foreground",
  "bg-tint-sage text-tint-sage-foreground",
  "bg-tint-rose text-tint-rose-foreground",
] as const;

const AI_TONES = [
  "bg-tint-teal text-tint-teal-foreground",
  "bg-tint-sky text-tint-sky-foreground",
  "bg-tint-indigo text-tint-indigo-foreground",
  "bg-tint-violet text-tint-violet-foreground",
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

/**
 * Token classes for the initials: a tint of the kind's family (cool for an AI, warm for anyone or
 * anything else), stable per id.
 */
export function avatarTone(id: string, kind?: IntelligenceKind): string {
  let hash = 0;
  for (let i = 0; i < id.length; i += 1) {
    hash = (hash * 31 + id.charCodeAt(i)) >>> 0;
  }
  const tones = kind === "ai" ? AI_TONES : PEOPLE_TONES;
  return tones[hash % tones.length] ?? tones[0];
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
          // The hairline, in the tint's own ink, keeps a pale face visible on a pale surface.
          "text-xs font-semibold tracking-wide uppercase ring-1 ring-current/25 ring-inset select-none",
          size === "xl" && "text-2xl",
          avatarTone(intelligence.id, intelligence.kind),
        )}
      >
        {initials(name)}
      </AvatarFallback>
    </Avatar>
  );
}
