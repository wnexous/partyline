import { IntelligenceAvatar, type IntelligenceAvatarProps } from "@/entities/intelligence";
import { groupFace } from "@/lib/conversations";
import { cn } from "@/lib/utils";

/*
 * A group's face (GAV-07): the photo its managers gave it, or its initials on the tint of its id.
 * It is a rounded square where a person or an AI is a circle, so a list that mixes them reads at a
 * glance.
 */

export interface GroupAvatarProps extends Omit<IntelligenceAvatarProps, "intelligence"> {
  group: { id: string; name: string; avatarUrl?: string };
}

export function GroupAvatar({ group, className, ...rest }: GroupAvatarProps) {
  return (
    <IntelligenceAvatar
      intelligence={groupFace(group.id, group.name, group.avatarUrl)}
      className={cn("rounded-lg *:rounded-lg", className)}
      {...rest}
    />
  );
}
