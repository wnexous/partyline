import type { Message } from "@/lib/api/types.gen";
import { isVoiceName } from "@/lib/voice";

/** A line is this many characters at most. */
const MAX = 140;

/**
 * One line that says what a message is, for the sheet of its actions and the strip of a reply
 * (ACT-05, ACT-07): its text, on one line and cut where it runs long, else what it carries (the
 * name of a recording is "Voice message", the name of any other file is its own).
 */
export function messageExcerpt(message: Message, voiceMessage: string): string {
  const text = message.text.replace(/\s+/g, " ").trim();
  if (text) {
    // By character, not by code unit: an emoji at the cut is not split in two.
    const chars = Array.from(text);
    return chars.length > MAX ? `${chars.slice(0, MAX).join("").trimEnd()}…` : text;
  }
  const file = message.attachments[0];
  if (!file) return "";
  return isVoiceName(file.name) ? voiceMessage : file.name;
}
