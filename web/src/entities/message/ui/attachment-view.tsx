import { FileIcon } from "lucide-react";
import type { Attachment } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { formatBytes } from "@/shared/lib/bytes";
import { attachmentUrl, INLINE_IMAGES } from "../lib/attachments";
import { isAudio } from "../lib/voice";
import { AudioPlayer } from "./audio-player";

/** Reserved box for inline images (Attachment has no width/height). */
const IMAGE_BOX = { width: 320, height: 240 } as const;
const IMAGE_BOX_RATIO = "4 / 3";

export function AttachmentView({
  attachment: a,
  onOpenImage,
}: {
  attachment: Attachment;
  onOpenImage: (attachmentId: string) => void;
}) {
  const { t, locale } = useI18n();
  const url = attachmentUrl(a.id);
  if (a.status !== "stored") {
    return (
      <span className="inline-flex max-w-full items-center gap-1.5 rounded-md border border-dashed px-2 py-1 text-xs text-muted-foreground">
        <FileIcon className="size-3.5 shrink-0" aria-hidden="true" />
        <span className="truncate">{a.name}</span>
        <span>
          ·{" "}
          {a.status === "too_large"
            ? t("message.attachmentTooLarge")
            : t("message.attachmentUnavailable")}
        </span>
      </span>
    );
  }
  if (INLINE_IMAGES.has(a.mime)) {
    return (
      // The image opens in the viewer inside the app (IMG-01), which hands focus back to this
      // button (`data-attachment-id` finds it again).
      <button
        type="button"
        data-attachment-id={a.id}
        aria-haspopup="dialog"
        aria-label={t("message.openImage", { name: a.name })}
        onClick={() => onOpenImage(a.id)}
        // Attachment carries no pixel size: a fixed 4:3 box keeps the row height stable while the
        // image loads (no layout shift, steady virtualizer measurements).
        className="inline-block w-80 max-w-full cursor-zoom-in overflow-hidden rounded-md border bg-muted align-top shadow-xs"
        style={{ aspectRatio: IMAGE_BOX_RATIO }}
      >
        <img
          src={url}
          alt={a.name}
          width={IMAGE_BOX.width}
          height={IMAGE_BOX.height}
          loading="lazy"
          decoding="async"
          className="block size-full object-contain"
        />
      </button>
    );
  }
  // A recording or any audio file plays in the row (VOI-07); the player falls back to a download.
  if (isAudio(a.mime)) return <AudioPlayer attachment={a} />;
  return (
    <a
      href={url}
      download={a.name}
      className="inline-flex max-w-full items-center gap-1.5 rounded-md border bg-card px-2 py-1 text-xs shadow-xs transition-colors hover:bg-accent"
    >
      <FileIcon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
      <span className="truncate font-medium">{a.name}</span>
      <span className="font-mono text-muted-foreground tabular">{formatBytes(a.size, locale)}</span>
    </a>
  );
}
