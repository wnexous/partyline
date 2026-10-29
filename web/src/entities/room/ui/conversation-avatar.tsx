import { IntelligenceAvatar, type IntelligenceAvatarProps } from "@/entities/intelligence/@x/room";
import type { Conversation, IntelligenceRef } from "@/shared/api/types.gen";
import { groupFace } from "../lib/conversations";

/** The face of a group: its photo, or its initials on the tint of its id (GAV-07), a rounded square. */
type GroupFace = { id: string; name: string; avatarUrl?: string };

type FaceProps = Omit<IntelligenceAvatarProps, "intelligence" | "shape">;

/**
 * A conversation's face: the other party of a DM, who looks like anyone else (a circle), or a
 * group's photo, with its initials as the fallback (a rounded square, so a list that mixes them
 * reads at a glance). The name always sits beside it, so it is decorative where it is used so.
 *
 * The variant is which of the two shapes it is given: `conversation` draws a row's conversation
 * (its counterpart or its own title and photo), `face` the counterpart or group a page names. It
 * is one component so a conversation has one face.
 */
export type ConversationAvatarProps = FaceProps &
  (
    | {
        variant: "conversation";
        conversation: Conversation;
        counterpart?: never;
        group?: never;
      }
    | {
        variant?: "face";
        counterpart?: IntelligenceRef;
        group: GroupFace;
        conversation?: never;
      }
  );

export function ConversationAvatar(props: ConversationAvatarProps) {
  const { variant = "face", conversation, counterpart, group, decorative = true, ...rest } = props;
  const face =
    variant === "conversation" && conversation
      ? {
          counterpart: conversation.counterpart,
          group: {
            id: conversation.id,
            name: conversation.title,
            avatarUrl: conversation.avatar_url,
          },
        }
      : { counterpart, group };
  if (!face.group) return null;
  return face.counterpart ? (
    <IntelligenceAvatar intelligence={face.counterpart} decorative={decorative} {...rest} />
  ) : (
    <IntelligenceAvatar
      intelligence={groupFace(face.group.id, face.group.name, face.group.avatarUrl)}
      shape="square"
      decorative={decorative}
      {...rest}
    />
  );
}
