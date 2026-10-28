import {
  ChevronLeftIcon,
  ChevronRightIcon,
  DownloadIcon,
  ExternalLinkIcon,
  ImageOffIcon,
  XIcon,
} from "lucide-react";
import { type KeyboardEvent, type PointerEvent, useEffect, useRef, useState } from "react";
import { isViewableImage } from "@/components/message-item";
import { Button } from "@/shared/ui/primitives/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@/shared/ui/primitives/dialog";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { attachmentUrl } from "@/lib/api/queries";
import type { Attachment, Message } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { isCoarsePointer } from "@/shared/lib/device";

/*
 * The image viewer (IMG-01..IMG-06): an image attachment opens in a dialog inside the app instead
 * of a new tab. The image is fitted to the viewport under a paper strip with its name, its place
 * among the conversation's images and the actions (download, open the original, close). Left and
 * right arrows, the edge buttons and a horizontal swipe walk the other images; Escape, the close
 * button and a click on the backdrop close it, and focus goes back to the image's thumbnail.
 * It is a modal dialog (focus trapped, the page behind hidden from assistive technology), built
 * on the shared Dialog.
 */

/** A swipe is at least this far sideways... */
const SWIPE_MIN_PX = 48;
/** ...and clearly more sideways than vertical, so a scroll or a pinch never changes the image. */
const SWIPE_DOMINANCE = 1.5;

/** An image of the conversation and the message it came with. */
export interface ViewerImage extends Attachment {
  messageId: string;
}

/**
 * The images the viewer walks (IMG-04): the stored PNG, JPEG, GIF and WebP attachments of the
 * messages, in message order. SVG and files the server did not store never open in the viewer.
 */
export function collectImages(messages: Message[]): ViewerImage[] {
  return messages.flatMap((message) =>
    message.attachments
      .filter(isViewableImage)
      .map((attachment) => ({ ...attachment, messageId: message.id })),
  );
}

export interface ImageViewerProps {
  /** The images to walk, in the order the arrows follow. */
  images: ViewerImage[];
  /** The image shown; null keeps the viewer closed. */
  currentId: string | null;
  /** Called with the image to show next, or null to close. */
  onCurrentChange: (id: string | null) => void;
  /** The element that stands for an image once the viewer closes; it takes focus back (IMG-03). */
  returnFocusTo?: (id: string) => HTMLElement | null | undefined;
  /** Older images may exist that are not in `images` yet: the position reads "2 of 5+". */
  more?: boolean;
}

/** What the viewer shows for one image, kept while the dialog fades out. */
interface View {
  image: ViewerImage;
  position: number;
  count: number;
  more: boolean;
  previousId?: string;
  nextId?: string;
}

const ACTION = "shrink-0 max-sm:size-11 max-sm:px-0";

export function ImageViewer({
  images,
  currentId,
  onCurrentChange,
  returnFocusTo,
  more = false,
}: ImageViewerProps) {
  const { t } = useI18n();
  const index = currentId === null ? -1 : images.findIndex((image) => image.id === currentId);
  const image = images[index];
  const live: View | null = image
    ? {
        image,
        position: index + 1,
        count: images.length,
        more,
        previousId: images[index - 1]?.id,
        nextId: images[index + 1]?.id,
      }
    : null;

  // The dialog fades out after `currentId` went back to null: it keeps showing what it showed.
  const [kept, setKept] = useState<View | null>(null);
  if (
    live &&
    (kept?.image.id !== live.image.id ||
      kept.position !== live.position ||
      kept.count !== live.count ||
      kept.more !== live.more)
  ) {
    setKept(live);
  }
  const view = live ?? kept;
  const lastId = useRef<string | null>(null);
  if (live) lastId.current = live.image.id;

  // An image that is no longer in the list (it went with its message) closes the viewer.
  useEffect(() => {
    if (currentId !== null && index < 0) onCurrentChange(null);
  }, [currentId, index, onCurrentChange]);

  const content = useRef<HTMLDivElement>(null);
  const previousId = live?.previousId;
  const nextId = live?.nextId;

  // The next and previous images load while this one is looked at, so moving on is instant.
  useEffect(() => {
    for (const id of [previousId, nextId]) {
      if (id) new Image().src = attachmentUrl(id);
    }
  }, [previousId, nextId]);

  // Reaching the first or last image turns the button that was pressed off: focus must not be
  // left on it (or lost to the page), or the arrow keys would stop working.
  const open = live !== null;
  // biome-ignore lint/correctness/useExhaustiveDependencies: it runs again whenever the image changes, which is when a button can turn off
  useEffect(() => {
    if (!open) return;
    const active = document.activeElement;
    const lost =
      !active ||
      active === document.body ||
      (active instanceof HTMLButtonElement && active.disabled);
    if (lost) content.current?.focus();
  }, [open, previousId, nextId]);

  const go = (step: -1 | 1) => {
    const target = step < 0 ? previousId : nextId;
    if (target) onCurrentChange(target);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
    if (event.key === "ArrowLeft") go(-1);
    else if (event.key === "ArrowRight") go(1);
    else return;
    event.preventDefault();
  };

  // A swipe starts on a finger or a pen, never on a mouse (whose drag has no meaning here).
  const swipe = useRef<{ x: number; y: number } | null>(null);
  // A click closes the viewer only when it began on the backdrop too, so letting go of a drag that
  // started on the image does not.
  const startedOnBackdrop = useRef(true);
  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    startedOnBackdrop.current = event.target === event.currentTarget;
    swipe.current = event.pointerType === "mouse" ? null : { x: event.clientX, y: event.clientY };
  };
  const onPointerUp = (event: PointerEvent<HTMLDivElement>) => {
    const start = swipe.current;
    swipe.current = null;
    if (!start) return;
    const dx = event.clientX - start.x;
    const dy = event.clientY - start.y;
    if (Math.abs(dx) >= SWIPE_MIN_PX && Math.abs(dx) > SWIPE_DOMINANCE * Math.abs(dy)) {
      go(dx < 0 ? 1 : -1);
    }
  };

  if (!view) return null;
  const { image: shown, position, count } = view;
  const url = attachmentUrl(shown.id);

  return (
    <Dialog
      open={open}
      onOpenChange={(open) => {
        if (!open) onCurrentChange(null);
      }}
    >
      <DialogContent
        ref={content}
        showCloseButton={false}
        touchFirst={isCoarsePointer()}
        onKeyDown={onKeyDown}
        // Focus starts on the dialog itself: Escape and the arrows work at once, and no action is
        // one stray Enter away.
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          if (event.target instanceof HTMLElement) event.target.focus();
        }}
        // The dialog has no trigger to hand focus back to: it goes to the image that was shown.
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          const id = lastId.current;
          if (id) returnFocusTo?.(id)?.focus();
        }}
        className="inset-0 flex w-auto max-w-none translate-x-0 translate-y-0 flex-col gap-0 rounded-none border-0 bg-transparent p-0 shadow-none sm:max-w-none"
      >
        <div className="flex shrink-0 items-center gap-1 border-b bg-popover px-3 py-2 text-popover-foreground sm:px-4">
          <div aria-live="polite" className="min-w-0 flex-1">
            <DialogTitle className="truncate text-sm leading-5 font-semibold">
              {shown.name}
            </DialogTitle>
            <p className="font-mono text-xs leading-4 text-muted-foreground tabular">
              {t(view.more ? "viewer.positionMore" : "viewer.position", { position, count })}
            </p>
          </div>
          <Button asChild variant="ghost" size="sm" className={ACTION}>
            <a href={url} download={shown.name}>
              <DownloadIcon aria-hidden="true" />
              <span className="max-sm:sr-only">{t("viewer.download")}</span>
            </a>
          </Button>
          <Button asChild variant="ghost" size="sm" className={ACTION}>
            <a href={url} target="_blank" rel="noopener noreferrer">
              <ExternalLinkIcon aria-hidden="true" />
              <span className="max-sm:sr-only">{t("viewer.openOriginal")}</span>
            </a>
          </Button>
          <DialogClose asChild>
            <Button
              variant="ghost"
              size="icon"
              className={cn(ACTION, "ml-1")}
              aria-label={t("common.close")}
            >
              <XIcon aria-hidden="true" />
            </Button>
          </DialogClose>
        </div>

        {/* The backdrop is a pointer shortcut: Escape and the close button do the same. */}
        {/* biome-ignore lint/a11y/noStaticElementInteractions: backdrop click and swipe are shortcuts with keyboard equivalents (Escape, the arrows, the buttons) */}
        {/* biome-ignore lint/a11y/useKeyWithClickEvents: closing has the keyboard equivalent Escape */}
        <div
          data-testid="image-viewer-backdrop"
          className="relative flex min-h-0 flex-1 touch-pan-y touch-pinch-zoom items-center justify-center overflow-hidden bg-black/80 p-3 sm:p-8"
          onPointerDown={onPointerDown}
          onPointerUp={onPointerUp}
          onPointerCancel={() => {
            swipe.current = null;
          }}
          onClick={(event) => {
            if (event.target === event.currentTarget && startedOnBackdrop.current) {
              onCurrentChange(null);
            }
          }}
        >
          <StageImage key={shown.id} image={shown} url={url} />
          {count > 1 ? (
            <>
              <Button
                variant="secondary"
                size="icon"
                className="absolute top-1/2 left-2 -translate-y-1/2 shadow-md ring-1 ring-border max-sm:size-11 sm:left-4"
                aria-label={t("viewer.previous")}
                disabled={position === 1}
                onClick={() => go(-1)}
              >
                <ChevronLeftIcon aria-hidden="true" />
              </Button>
              <Button
                variant="secondary"
                size="icon"
                className="absolute top-1/2 right-2 -translate-y-1/2 shadow-md ring-1 ring-border max-sm:size-11 sm:right-4"
                aria-label={t("viewer.next")}
                disabled={position === count}
                onClick={() => go(1)}
              >
                <ChevronRightIcon aria-hidden="true" />
              </Button>
            </>
          ) : null}
        </div>
        <DialogDescription className="sr-only">
          {t(count > 1 ? "viewer.description" : "viewer.descriptionOne")}
        </DialogDescription>
      </DialogContent>
    </Dialog>
  );
}

/**
 * The picture itself, fitted inside the stage without cropping or stretching. It restarts for
 * every image (`key`): a skeleton stands in while it loads and a failure is said in words, with
 * the header's actions still at hand.
 */
function StageImage({ image, url }: { image: ViewerImage; url: string }) {
  const { t } = useI18n();
  const [state, setState] = useState<"loading" | "loaded" | "failed">("loading");

  if (state === "failed") {
    return (
      <div
        role="status"
        className="flex max-w-sm flex-col items-center gap-2 rounded-lg border bg-popover p-6 text-center text-sm text-popover-foreground shadow-md"
      >
        <ImageOffIcon className="size-6 text-muted-foreground" aria-hidden="true" />
        <p>{t("viewer.loadError")}</p>
      </div>
    );
  }
  return (
    <>
      {state === "loading" ? (
        <Skeleton aria-hidden="true" className="absolute aspect-[4/3] w-80 max-w-[80%]" />
      ) : null}
      <img
        src={url}
        alt={image.name}
        draggable={false}
        decoding="async"
        onLoad={() => setState("loaded")}
        onError={() => setState("failed")}
        className="relative max-h-full max-w-full rounded-sm object-contain shadow-lg animate-in duration-150 fade-in-0"
      />
    </>
  );
}
