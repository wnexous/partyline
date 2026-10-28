/*
 * The emoji a message is reacted with (RCT-01, RCT-02): the six at hand and the picker's larger set.
 * The rules of a reaction list live in entities/message (lib/reaction-list.ts).
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
