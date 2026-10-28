import { MaxMessageEmoji, type Reaction } from "@/shared/api/types.gen";

/*
 * The emoji a message is reacted with (RCT-01, RCT-02) and what one reaction does to the list a message
 * carries. The server keeps the truth (`Message.reactions`, in the order each emoji was first used); these are
 * the rules that let a tap show its result before the answer comes (RCT-08).
 */

/** The six at hand: the bar of a menu or a sheet, and the toolbar of a row under a mouse. */
export const QUICK_REACTIONS = ["👍", "❤️", "😂", "😮", "😢", "🙏"] as const;

/** How many emoji a row of the picker holds. */
export const PICKER_COLUMNS = 8;

/**
 * The rest, behind "More": what a chat about work reaches for after the six, laid out in rows of eight. The six
 * are not repeated, as they sit beside the button that opens these. Every one is a single emoji of Unicode 13 or
 * older, so a system without the newest fonts draws them all.
 */
export const MORE_REACTIONS: readonly string[] = [
  "👎",
  "🔥",
  "🎉",
  "👏",
  "🙌",
  "💪",
  "👀",
  "💯",
  "✅",
  "❌",
  "⚠️",
  "❓",
  "🚀",
  "⭐",
  "✨",
  "💥",
  "😀",
  "😅",
  "😉",
  "🙂",
  "😍",
  "😘",
  "😎",
  "🤔",
  "😭",
  "😡",
  "😴",
  "🤯",
  "🥳",
  "🤝",
  "👋",
  "🤗",
  "🎯",
  "💡",
  "⏳",
  "📌",
  "🐛",
  "🔧",
  "☕",
  "🍕",
  "🎂",
  "🙈",
  "🤣",
  "😊",
  "💔",
  "🤖",
  "🧪",
  "🏆",
];

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
