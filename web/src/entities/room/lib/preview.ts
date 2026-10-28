import {
  type ConversationKind,
  MaxPreviewLength,
  type Message,
  type MessagePreview,
} from "@/shared/api/types.gen";

/** The first `max` code points of `text`: an emoji counts once, whatever its UTF-16 length. */
function firstCodePoints(text: string, max: number): string {
  let end = 0;
  let count = 0;
  for (const point of text) {
    if (count === max) break;
    end += point.length;
    count += 1;
  }
  return text.slice(0, end);
}

/**
 * The sidebar preview of a message (LAY-01), built as the server builds the list's: the first
 * MaxPreviewLength code points; "[first file name]" when the text is blank and there is a file;
 * the sender's name in groups only.
 */
export function previewOf(m: Message, kind: ConversationKind): MessagePreview {
  const file = m.attachments[0]?.name;
  const blank = m.text.trim() === "";
  const deleted = m.deleted_at !== undefined;
  return {
    seq: m.seq,
    author_id: m.author.id,
    author_name: kind === "group" ? m.author_name : "",
    // A deleted message previews nothing of what it said (DLT-05); the list says "Message deleted".
    text: deleted
      ? ""
      : blank
        ? file
          ? `[${file}]`
          : ""
        : firstCodePoints(m.text, MaxPreviewLength),
    created_at: m.created_at,
    ...(deleted ? { deleted: true } : {}),
  };
}
