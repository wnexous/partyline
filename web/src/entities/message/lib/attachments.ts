import type { Attachment } from "@/shared/api/types.gen";
export const attachmentUrl = (id: string) => `/api/v1/attachments/${id}`;

/** Images the server serves inline (others download); SVG is excluded on purpose. */
export const INLINE_IMAGES = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);
/** An image the timeline shows inline and the viewer can open: stored, and one the server serves inline. */
export const isViewableImage = (a: Attachment) =>
  a.status === "stored" && INLINE_IMAGES.has(a.mime);
