import { GroupAvatar } from "@/components/group-avatar";
import {
  IntelligenceAvatar,
  type IntelligenceAvatarProps,
} from "@/components/intelligence/intelligence-avatar";
import type { Conversation } from "@/lib/api/types.gen";

/**
 * The face of a row of the conversation list: the other party of a DM, who looks like anyone
 * else, or a group's photo, with its initials as the fallback (GAV-07). The name always sits
 * beside it, so it is decorative.
 */
export function ConversationAvatar({
  conversation: c,
  ...rest
}: { conversation: Conversation } & Omit<IntelligenceAvatarProps, "intelligence" | "decorative">) {
  return c.counterpart ? (
    <IntelligenceAvatar intelligence={c.counterpart} decorative {...rest} />
  ) : (
    <GroupAvatar
      group={{ id: c.id, name: c.title, avatarUrl: c.avatar_url }}
      decorative
      {...rest}
    />
  );
}
