import type { Attachment, AttachmentStatus } from "@/shared/api/types.gen";
import type { MessageKey } from "@/shared/i18n";

export const attachmentUrl = (id: string) => `/api/v1/attachments/${id}`;

/** Images the server serves inline (others download); SVG is excluded on purpose. */
export const INLINE_IMAGES = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);
/** An image the timeline shows inline and the viewer can open: stored, and one the server serves inline. */
export const isViewableImage = (a: Attachment) =>
  a.status === "stored" && INLINE_IMAGES.has(a.mime);

/**
 * Why a file that is not stored has no content, in the card the timeline and the files list show
 * instead of a link: too large to keep, lost on the way, or deleted by the instance's attachment
 * retention (RET-07).
 */
export function unstoredLabel(status: AttachmentStatus): MessageKey {
  switch (status) {
    case "too_large":
      return "message.attachmentTooLarge";
    case "expired":
      return "message.attachmentExpired";
    default:
      return "message.attachmentUnavailable";
  }
}
