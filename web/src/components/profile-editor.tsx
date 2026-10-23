import { zodResolver } from "@hookform/resolvers/zod";
import { type ChangeEvent, useEffect, useId, useRef, useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { FormAlert, TextAreaField, TextField } from "@/components/form-fields";
import { IntelligenceAvatar } from "@/components/intelligence/intelligence-avatar";
import { Button, buttonVariants } from "@/components/ui/button";
import { Form } from "@/components/ui/form";
import { isApiError } from "@/lib/api/client";
import { useRemoveAvatar, useUpdateIntelligence, useUploadAvatar } from "@/lib/api/queries";
import {
  ErrorCodeAvatarInvalid,
  ErrorCodeHandleTaken,
  type IntelligenceProfile,
  MaxProfileBio,
  type UpdateProfileRequest,
} from "@/lib/api/types.gen";
import { prepareAvatar } from "@/lib/avatar";
import { applyFieldErrors } from "@/lib/forms";
import { type Translate, useT } from "@/lib/i18n";
import { type ProfileEditForm, profileEditSchema } from "@/lib/schemas";
import { cn } from "@/lib/utils";

/*
 * The editor of a profile (PROF-03, PROF-05, PROF-11): the photo, the display name, the @handle
 * and the bio. It is for whoever may edit this intelligence (`profile.can_edit`): the user for
 * themselves, the owner for an AI, the users of a connector for its people. The caller decides
 * whether to show it; the server decides what is allowed.
 */

const PHOTO_TYPES = "image/png,image/jpeg,image/webp";

export function ProfileEditor({ profile }: { profile: IntelligenceProfile }) {
  const t = useT();
  const headingId = useId();
  return (
    <section aria-labelledby={headingId} className="space-y-5">
      <h2 id={headingId} className="text-lg font-semibold tracking-[-0.015em]">
        {t("profile.edit")}
      </h2>
      <div className="grid gap-6 sm:grid-cols-[auto_minmax(0,1fr)] sm:gap-8">
        <PhotoEditor profile={profile} />
        <ProfileForm profile={profile} />
      </div>
    </section>
  );
}

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
 * The photo: the current one with a way to remove it, and a picker. A chosen file is sent at once,
 * cropped to a circle as it will be; the server's refusal (not an image it can use) stays next to
 * the preview, so the old photo is never lost. A big photo is resized first, and an upload that
 * stalls is abandoned, so the sending state always ends.
 */
function PhotoEditor({ profile }: { profile: IntelligenceProfile }) {
  const t = useT();
  const hintId = useId();
  const upload = useUploadAvatar(profile.id);
  const remove = useRemoveAvatar(profile.id);
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
      // Sent now, not on the form's Save: the photo must not wait for a button that may never come.
      await upload.mutateAsync(image);
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
    remove.mutate(undefined, {
      onSuccess: () => toast.success(t("profile.photoRemoved")),
      onError: () => setError(t("profile.photoRemoveFailed")),
    });
  };

  return (
    <div className="flex flex-col gap-3 sm:w-44">
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
        <IntelligenceAvatar intelligence={profile} size="xl" decorative />
      )}
      <div className="flex flex-wrap gap-2">
        <label
          className={cn(
            buttonVariants({ variant: "outline", size: "sm" }),
            "cursor-pointer has-[:focus-visible]:border-ring has-[:focus-visible]:ring-[3px] has-[:focus-visible]:ring-ring/50",
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
        {profile.avatar_url ? (
          <Button variant="ghost" size="sm" disabled={remove.isPending} onClick={clear}>
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

const valuesOf = (p: IntelligenceProfile): ProfileEditForm => ({
  display_name: p.display_name,
  handle: p.handle,
  bio: p.bio,
});

/**
 * The text fields. Only what changed is sent. A taken handle and the server's reasons for a field
 * show under it, a refusal to edit or a network failure above the form. A change made elsewhere
 * (another tab, the AI itself) reaches the fields the user has not touched and leaves an edited
 * one as typed: the form is reset only when the stored profile differs from the one it last saw,
 * never on mount, and `keepDirtyValues` reads which fields were edited from the form itself.
 */
function ProfileForm({ profile }: { profile: IntelligenceProfile }) {
  const t = useT();
  const update = useUpdateIntelligence(profile.id);
  const [error, setError] = useState<string | null>(null);
  const form = useForm<ProfileEditForm>({
    resolver: zodResolver(profileEditSchema),
    defaultValues: valuesOf(profile),
  });
  const { isDirty } = form.formState;
  const { display_name, handle, bio } = profile;
  const seen = useRef({ display_name, handle, bio });

  useEffect(() => {
    const last = seen.current;
    if (last.display_name === display_name && last.handle === handle && last.bio === bio) return;
    seen.current = { display_name, handle, bio };
    form.reset({ display_name, handle, bio }, { keepDirtyValues: true });
  }, [form, display_name, handle, bio]);

  const onSubmit = form.handleSubmit(async (values) => {
    setError(null);
    const next = profileEditSchema.parse(values);
    const changes: UpdateProfileRequest = {};
    if (next.display_name !== profile.display_name) changes.display_name = next.display_name;
    if (next.handle !== profile.handle) changes.handle = next.handle;
    if (next.bio !== profile.bio) changes.bio = next.bio;
    if (Object.keys(changes).length === 0) {
      form.reset(valuesOf(profile));
      return;
    }
    try {
      const saved = await update.mutateAsync(changes);
      form.reset(valuesOf(saved));
      toast.success(t("account.profileSaved"));
    } catch (err) {
      if (isApiError(err) && err.code === ErrorCodeHandleTaken) {
        form.setError("handle", { type: "server", message: t("profile.handleTaken") });
        return;
      }
      if (applyFieldErrors(err, form.setError, ["display_name", "handle", "bio"])) return;
      if (isApiError(err) && err.status === 403) setError(t("profile.forbidden"));
      else setError(isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic"));
    }
  });

  return (
    <Form {...form}>
      <form onSubmit={onSubmit} noValidate className="min-w-0 max-w-xl space-y-4">
        <FormAlert>{error}</FormAlert>
        <TextField
          control={form.control}
          name="display_name"
          label={t("auth.displayName")}
          autoComplete="off"
        />
        <TextField
          control={form.control}
          name="handle"
          label={t("profile.handle")}
          description={t("profile.handleHint")}
          autoComplete="off"
        />
        <TextAreaField
          control={form.control}
          name="bio"
          label={t("profile.bio")}
          max={MaxProfileBio}
        />
        <Button type="submit" disabled={!isDirty || update.isPending}>
          {t("account.saveProfile")}
        </Button>
      </form>
    </Form>
  );
}
