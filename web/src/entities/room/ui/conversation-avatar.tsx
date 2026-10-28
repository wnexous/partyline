import { IntelligenceAvatar, type IntelligenceAvatarProps } from "@/entities/intelligence/@x/room";
import type { Conversation, IntelligenceRef } from "@/shared/api/types.gen";
import { groupFace } from "../lib/conversations";

/** The face of a group: its photo, or its initials on the tint of its id (GAV-07), a rounded square. */
export interface ConversationFaceProps
  extends Omit<IntelligenceAvatarProps, "intelligence" | "shape"> {
  /** The other party of a DM; undefined for a group. */
  counterpart?: IntelligenceRef;
  group: { id: string; name: string; avatarUrl?: string };
}

/**
 * A conversation's face: the other party of a DM, who looks like anyone else (a circle), or a
 * group's photo, with its initials as the fallback (a rounded square, so a list that mixes them
 * reads at a glance). The name always sits beside it, so it is decorative where it is used so.
 */
export function ConversationFace({ counterpart, group, ...rest }: ConversationFaceProps) {
  return counterpart ? (
    <IntelligenceAvatar intelligence={counterpart} {...rest} />
  ) : (
    <IntelligenceAvatar
      intelligence={groupFace(group.id, group.name, group.avatarUrl)}
      shape="square"
      {...rest}
    />
  );
}

/** The face of a row of the conversation list (GAV-07). */
export function ConversationAvatar({
  conversation: c,
  ...rest
}: { conversation: Conversation } & Omit<ConversationFaceProps, "counterpart" | "group">) {
  return (
    <ConversationFace
      counterpart={c.counterpart}
      group={{ id: c.id, name: c.title, avatarUrl: c.avatar_url }}
      decorative
      {...rest}
    />
  );
}
