import { avatarTone, initials } from "@/shared/lib/avatar";
import { cn } from "@/shared/lib/utils";
import { Avatar, AvatarFallback, AvatarImage } from "@/shared/ui/primitives/avatar";

/*
 * A face (PROF-08): a picture, or initials on a soft tint picked from the design tokens by a hash
 * of its id. Only the id drives the tint, never the kind: people, AIs, groups and organizations
 * are drawn from the same palette and look alike, and a rename keeps the face. Red is for errors
 * and green for presence, so neither is a tint. A circle is a person or an AI; a rounded square is
 * a group or an organization, so a list that mixes them reads at a glance.
 */

/** The pixel size of each face, so the browser reserves the box before the image loads. */
const PIXELS = { sm: 24, default: 32, lg: 40, xl: 96 } as const;

export type FaceSize = keyof typeof PIXELS;

export interface FaceProps {
  /** Drives the tint: the same id always gets the same one. */
  id: string;
  name: string;
  imageUrl?: string;
  /** `xl` is a profile page's: 96 px. */
  size?: FaceSize;
  shape?: "circle" | "square";
  /** Hidden from assistive technology: the name sits right next to the face (rows, headers). */
  decorative?: boolean;
  className?: string;
  /** Extra classes on the initials, such as the hairline that keeps a pale face visible. */
  fallbackClassName?: string;
  testId?: string;
}

export function Face({
  id,
  name,
  imageUrl,
  size = "default",
  shape = "circle",
  decorative = false,
  className,
  fallbackClassName,
  testId,
}: FaceProps) {
  const label = name.trim() || "?";
  return (
    <Avatar
      size={size === "xl" ? "lg" : size}
      data-testid={testId}
      aria-hidden={decorative || undefined}
      className={cn(
        "shrink-0",
        shape === "square" && "rounded-lg *:rounded-lg",
        size === "xl" && "data-[size=lg]:size-24",
        className,
      )}
    >
      {imageUrl ? (
        <AvatarImage
          src={imageUrl}
          alt={decorative ? "" : label}
          width={PIXELS[size]}
          height={PIXELS[size]}
        />
      ) : null}
      <AvatarFallback
        className={cn(
          "text-xs font-semibold tracking-wide uppercase select-none",
          size === "xl" && "text-2xl",
          avatarTone(id),
          fallbackClassName,
        )}
      >
        {initials(label)}
      </AvatarFallback>
    </Avatar>
  );
}
