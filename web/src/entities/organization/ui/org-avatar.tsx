import { cn } from "@/shared/lib/utils";
import { Face } from "@/shared/ui/atoms/face";

/*
 * An organization's face: the picture its admins gave it, or initials on a grey picked from the
 * design tokens by a hash of its id (the same tints the intelligences use). It is a rounded
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

export function OrgAvatar({ org, size = "default", className, decorative = true }: OrgAvatarProps) {
  return (
    <Face
      id={org.id}
      name={org.name}
      imageUrl={org.avatarUrl}
      size={size}
      shape="square"
      decorative={decorative}
      // A group and an organization share the square base; the organization's corners are a little tighter.
      className={cn("rounded-md *:rounded-md", className)}
      fallbackClassName="rounded-md"
      testId="org-avatar"
    />
  );
}
