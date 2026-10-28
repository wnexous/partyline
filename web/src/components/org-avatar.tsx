import { avatarTone, initials } from "@/shared/lib/avatar";
import { cn } from "@/shared/lib/utils";
import { Avatar, AvatarFallback, AvatarImage } from "@/shared/ui/primitives/avatar";

/*
 * An organization's face: the picture its admins gave it, or initials on a grey picked from the
 * design tokens by a hash of its id (the same greys the intelligences use). It is a rounded
 * square where a person or an AI is a circle, so a list that mixes them tells them apart.
 */

export interface OrgAvatarProps {
  org: { id: string; name: string; avatarUrl?: string };
  /** `xl` is the settings page's: 96 px. */
  size?: "default" | "sm" | "lg" | "xl";
  className?: string;
  /** Hidden from assistive technology: the name sits right next to the face (rows, menus). */
  decorative?: boolean;
}

const PIXELS = { sm: 24, default: 32, lg: 40, xl: 96 } as const;

export function OrgAvatar({ org, size = "default", className, decorative = true }: OrgAvatarProps) {
  const name = org.name.trim() || "?";
  return (
    <Avatar
      size={size === "xl" ? "lg" : size}
      data-testid="org-avatar"
      aria-hidden={decorative || undefined}
      className={cn("shrink-0 rounded-md", size === "xl" && "data-[size=lg]:size-24", className)}
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
          size === "xl" && "text-2xl",
          avatarTone(org.id),
        )}
      >
        {initials(name)}
      </AvatarFallback>
    </Avatar>
  );
}
