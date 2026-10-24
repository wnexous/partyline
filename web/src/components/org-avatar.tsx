import { avatarTone, initials } from "@/components/intelligence/intelligence-avatar";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import { cn } from "@/lib/utils";

/*
 * An organization's face: the picture its admins gave it, or initials on a grey picked from the
 * design tokens by a hash of its id (the same greys the intelligences use). It is a rounded
 * square where a person or an AI is a circle, so a list that mixes them tells them apart.
 */

export interface OrgAvatarProps {
  org: { id: string; name: string; avatarUrl?: string };
  size?: "default" | "sm" | "lg";
  className?: string;
  /** Hidden from assistive technology: the name sits right next to the face (rows, menus). */
  decorative?: boolean;
}

const PIXELS = { sm: 24, default: 32, lg: 40 } as const;

export function OrgAvatar({ org, size = "default", className, decorative = true }: OrgAvatarProps) {
  const name = org.name.trim() || "?";
  return (
    <Avatar
      size={size}
      data-testid="org-avatar"
      aria-hidden={decorative || undefined}
      className={cn("shrink-0 rounded-md", className)}
    >
      {org.avatarUrl ? (
        <AvatarImage
          src={org.avatarUrl}
          alt={decorative ? "" : name}
          width={PIXELS[size]}
          height={PIXELS[size]}
          className="rounded-md"
        />
      ) : null}
      <AvatarFallback
        className={cn(
          "rounded-md text-xs font-semibold tracking-wide uppercase select-none",
          avatarTone(org.id),
        )}
      >
        {initials(name)}
      </AvatarFallback>
    </Avatar>
  );
}
