/*
 * The rules of one message's reaction list (RCT-01, RCT-02, RCT-04, RCT-08): whom a reaction
 * belongs to, what one tap does to the list a message carries, and when the server would refuse
 * a new emoji. The server keeps the truth (`Message.reactions`); these let a tap show its result
 * before the answer comes. The emoji sets the picker offers live in features/react-to-message.
 */

import { MaxMessageEmoji, type Reaction } from "@/shared/api/types.gen";

/** Whether the intelligence `iid` reacted with `emoji`. */
export const reactedWith = (reactions: Reaction[], emoji: string, iid: string) =>
  reactions.some((r) => r.emoji === emoji && r.by.includes(iid));

/**
 * The list once `iid` reacts with `emoji` (`add`) or takes that reaction back. A new emoji goes last and a
 * person goes after those who used it before them, as on the server; an emoji nobody uses any more is gone.
 * It answers the same list when there is nothing to change, so a repeat renders nothing.
 */
export function withReaction(
  reactions: Reaction[],
  emoji: string,
  iid: string,
  add: boolean,
): Reaction[] {
  const at = reactions.findIndex((r) => r.emoji === emoji);
  const current = at < 0 ? undefined : reactions[at];
  if (add) {
    if (!current) return [...reactions, { emoji, by: [iid] }];
    if (current.by.includes(iid)) return reactions;
    return reactions.map((r, i) => (i === at ? { ...r, by: [...r.by, iid] } : r));
  }
  if (!current?.by.includes(iid)) return reactions;
  const by = current.by.filter((id) => id !== iid);
  return by.length === 0
    ? reactions.filter((_, i) => i !== at)
    : reactions.map((r, i) => (i === at ? { ...r, by } : r));
}

/** A new emoji on a message that carries the most different ones already (RCT-04): the server refuses it. */
export const atReactionLimit = (reactions: Reaction[], emoji: string) =>
  reactions.length >= MaxMessageEmoji && !reactions.some((r) => r.emoji === emoji);
