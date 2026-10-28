import { zodResolver } from "@hookform/resolvers/zod";
import { useEffect, useId, useRef, useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { PhotoEditor } from "@/components/photo-editor";
import {
  IntelligenceAvatar,
  useRemoveAvatar,
  useUpdateIntelligence,
  useUploadAvatar,
} from "@/entities/intelligence";
import { type ProfileEditForm, profileEditSchema } from "@/lib/schemas";
import { isApiError } from "@/shared/api/client";
import {
  ErrorCodeHandleTaken,
  type IntelligenceProfile,
  MaxProfileBio,
  type UpdateProfileRequest,
} from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { applyFieldErrors } from "@/shared/lib/forms";
import { FormAlert, TextAreaField, TextField } from "@/shared/ui/molecules/form-fields";
import { Button } from "@/shared/ui/primitives/button";
import { Form } from "@/shared/ui/primitives/form";

/*
 * The editor of a profile (PROF-03, PROF-05, PROF-11): the photo, the display name, the @handle
 * and the bio. It is for whoever may edit this intelligence (`profile.can_edit`): the user for
 * themselves, the owner for an AI, the users of a connector for its people. The caller decides
 * whether to show it; the server decides what is allowed.
 */

export function ProfileEditor({ profile }: { profile: IntelligenceProfile }) {
  const t = useT();
  const headingId = useId();
  return (
    <section aria-labelledby={headingId} className="space-y-5">
      <h2 id={headingId} className="text-lg font-semibold tracking-[-0.015em]">
        {t("profile.edit")}
      </h2>
      <div className="grid gap-6 sm:grid-cols-[auto_minmax(0,1fr)] sm:gap-8">
        <ProfilePhoto profile={profile} />
        <ProfileForm profile={profile} />
      </div>
    </section>
  );
}

/** The profile's photo: the editor every picture shares, wired to this intelligence's routes. */
function ProfilePhoto({ profile }: { profile: IntelligenceProfile }) {
  const upload = useUploadAvatar(profile.id);
  const remove = useRemoveAvatar(profile.id);
  return (
    <PhotoEditor
      face={<IntelligenceAvatar intelligence={profile} size="xl" decorative />}
      hasPhoto={Boolean(profile.avatar_url)}
      upload={upload.mutateAsync}
      remove={(done) => remove.mutate(undefined, done)}
      removing={remove.isPending}
    />
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
