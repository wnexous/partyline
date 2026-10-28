import { type ChangeEvent, type ReactNode, useEffect, useId, useRef, useState } from "react";
import { toast } from "sonner";
import { Button, buttonVariants } from "@/shared/ui/primitives/button";
import { isApiError } from "@/lib/api/client";
import { ErrorCodeAvatarInvalid } from "@/lib/api/types.gen";
import { prepareAvatar } from "@/shared/lib/avatar";
import { type Translate, useT } from "@/lib/i18n";
import { cn } from "@/shared/lib/utils";

const PHOTO_TYPES = "image/png,image/jpeg,image/webp";

/** Why a photo was refused, in words the user can act on; a lost connection ends like any other
 * failed upload. */
function photoError(t: Translate, err: unknown): string {
  if (isApiError(err)) {
    if (err.code === ErrorCodeAvatarInvalid) return t("profile.photoInvalid");
    if (err.status === 413) return t("profile.photoTooLarge");
  }
  return t("profile.photoFailed");
}

/**
 * The photo of an intelligence, an organization or a group: the current one with a way to remove it,
 * and a picker. A chosen file is sent at once, cropped as it will be shown; the server's refusal (not an
 * image it can use) stays next to the preview, so the old photo is never lost. A big photo is
 * resized first, and an upload that stalls is abandoned by `upload`, so the sending state always
 * ends. What the picture belongs to is the caller's: it passes the current face and the calls.
 */
export function PhotoEditor({
  face,
  hasPhoto,
  upload,
  remove,
  removing,
  className,
}: {
  /** The current picture, shown while no new file is chosen. */
  face: ReactNode;
  hasPhoto: boolean;
  /** Sends the prepared image; rejects with the server's refusal. */
  upload: (image: Blob) => Promise<unknown>;
  /** Removes the current picture and says how it went. */
  remove: (done: { onSuccess: () => void; onError: () => void }) => void;
  removing: boolean;
  /** Where the editor sits decides its width: a narrow column beside a form, the whole width alone. */
  className?: string;
}) {
  const t = useT();
  const hintId = useId();
  const [picked, setPicked] = useState<{ file: File; url: string } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  // Which choice the pending answer belongs to: a newer choice ignores an older answer.
  const attempt = useRef(0);

  // The preview address lives as long as the preview does.
  useEffect(() => {
    const url = picked?.url;
    return () => {
      if (url) URL.revokeObjectURL(url);
    };
  }, [picked]);

  const choose = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    // So that choosing the same file again still counts as a change.
    e.target.value = "";
    if (!file) return;
    const current = ++attempt.current;
    setError(null);
    setPicked({ file, url: URL.createObjectURL(file) });
    setSending(true);
    try {
      let image: Blob;
      try {
        image = await prepareAvatar(file);
      } catch {
        if (attempt.current !== current) return;
        setError(t("profile.photoInvalid"));
        return;
      }
      if (attempt.current !== current) return;
      // Sent now, not on a form's Save: the photo must not wait for a button that may never come.
      await upload(image);
      if (attempt.current !== current) return;
      setPicked(null);
      toast.success(t("profile.photoUpdated"));
    } catch (err) {
      if (attempt.current !== current) return;
      setError(photoError(t, err));
    } finally {
      if (attempt.current === current) setSending(false);
    }
  };

  const clear = () => {
    attempt.current += 1;
    setError(null);
    setPicked(null);
    setSending(false);
    remove({
      onSuccess: () => toast.success(t("profile.photoRemoved")),
      onError: () => setError(t("profile.photoRemoveFailed")),
    });
  };

  return (
    <div className={cn("flex flex-col gap-3 sm:w-44", className)}>
      <h3 className="text-sm font-medium">{t("profile.photo")}</h3>
      {picked ? (
        <img
          src={picked.url}
          alt={t("profile.photoPreview")}
          width={96}
          height={96}
          className="size-24 rounded-full object-cover ring-2 ring-tape-strong"
        />
      ) : (
        face
      )}
      <div className="flex flex-wrap gap-2">
        <label
          className={cn(
            buttonVariants({ variant: "outline", size: "sm" }),
            "cursor-pointer has-[:focus-visible]:border-ring has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/70",
          )}
        >
          <input
            type="file"
            accept={PHOTO_TYPES}
            aria-describedby={hintId}
            onChange={choose}
            className="sr-only"
          />
          {t("profile.photoUpload")}
        </label>
        {hasPhoto ? (
          <Button type="button" variant="ghost" size="sm" disabled={removing} onClick={clear}>
            {t("profile.photoRemove")}
          </Button>
        ) : null}
      </div>
      <p id={hintId} className="text-xs text-pretty text-muted-foreground">
        {t("profile.photoHint")}
      </p>
      {sending ? (
        <p role="status" className="text-xs text-muted-foreground">
          {t("profile.photoSending")}
        </p>
      ) : null}
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  );
}
