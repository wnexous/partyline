import { zodResolver } from "@hookform/resolvers/zod";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useEffect, useId, useRef, useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { PhotoEditor } from "@/components/photo-editor";
import { useMe } from "@/entities/account";
import {
  isAdminOf,
  OrgAvatar,
  ownedThings,
  useDeleteOrg,
  useOrg,
  useRemoveFromOrg,
  useRemoveOrgAvatar,
  useUpdateOrg,
  useUploadOrgAvatar,
} from "@/entities/organization";
import { type UpdateOrgForm, updateOrgSchema } from "@/lib/schemas";
import { isApiError } from "@/shared/api/client";
import {
  ErrorCodeLastAdmin,
  ErrorCodeOrganizationNotEmpty,
  type Organization,
  type UpdateOrganizationRequest,
} from "@/shared/api/types.gen";
import { useI18n, useT } from "@/shared/i18n";
import { applyFieldErrors } from "@/shared/lib/forms";
import { ConfirmDialog } from "@/shared/ui/molecules/confirm-dialog";
import { FormAlert, TextField } from "@/shared/ui/molecules/form-fields";
import { PageHeader } from "@/shared/ui/molecules/page-header";
import { PageSection as Section } from "@/shared/ui/molecules/page-section";
import { Button } from "@/shared/ui/primitives/button";
import { Form } from "@/shared/ui/primitives/form";

export const Route = createFileRoute("/_app/orgs/$orgId/settings")({
  component: OrgSettingsPage,
});

/*
 * What an organization is called, its address and its photo (OMEM-04: only admins change them),
 * and the two ways out of it: leaving (anyone, but the only admin: OMEM-05) and deleting it
 * (admins, and only when it owns nothing: ORG-06).
 */
function OrgSettingsPage() {
  const t = useT();
  const { orgId } = Route.useParams();
  // The layout has loaded it and shows the failures.
  const org = useOrg(orgId).data;
  if (!org) return null;
  const admin = isAdminOf(org);
  return (
    <>
      <PageHeader
        title={t("orgs.settingsTitle")}
        description={t(admin ? "orgs.settingsBodyAdmin" : "orgs.settingsBodyMember", {
          org: org.name,
        })}
      />
      <div className="max-w-2xl divide-y px-4 md:px-8">
        <DetailsSection org={org} admin={admin} />
        <LeaveSection org={org} />
        {admin ? <DeleteSection org={org} /> : null}
      </div>
    </>
  );
}

const faceOf = (org: Organization) => ({
  id: org.id,
  name: org.name,
  ...(org.avatar_url ? { avatarUrl: org.avatar_url } : {}),
});

function DetailsSection({ org, admin }: { org: Organization; admin: boolean }) {
  const t = useT();
  const id = useId();
  return (
    <Section
      id={`${id}-details`}
      title={t("orgs.details")}
      description={t("orgs.detailsBody", { org: org.name })}
    >
      {admin ? <DetailsEditor org={org} /> : <DetailsFacts org={org} />}
    </Section>
  );
}

/** What a member sees: the same facts, with nothing to change. */
function DetailsFacts({ org }: { org: Organization }) {
  const t = useT();
  return (
    <div className="flex flex-wrap items-start gap-6">
      <OrgAvatar org={faceOf(org)} size="xl" />
      <dl className="min-w-0 space-y-3 text-sm">
        <div>
          <dt className="text-xs text-muted-foreground">{t("orgs.name")}</dt>
          <dd className="font-medium break-words">{org.name}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">{t("orgs.slug")}</dt>
          <dd className="font-mono break-all">{org.slug}</dd>
        </div>
      </dl>
    </div>
  );
}

const valuesOf = (o: Organization): UpdateOrgForm => ({ name: o.name, slug: o.slug });

/** What an admin sees: the photo, sent at once, and the name and address, saved together. */
function DetailsEditor({ org }: { org: Organization }) {
  const upload = useUploadOrgAvatar(org.id);
  const remove = useRemoveOrgAvatar(org.id);
  return (
    <div className="grid gap-6 sm:grid-cols-[auto_minmax(0,1fr)] sm:gap-8">
      <PhotoEditor
        face={<OrgAvatar org={faceOf(org)} size="xl" />}
        hasPhoto={Boolean(org.avatar_url)}
        upload={upload.mutateAsync}
        remove={(done) => remove.mutate(undefined, done)}
        removing={remove.isPending}
      />
      <DetailsForm org={org} />
    </div>
  );
}

/**
 * The name and the address. Only what changed is sent. A taken address and the server's reasons
 * for a field show under it, a refusal or a lost connection above the form. A change made elsewhere
 * reaches the fields not touched and leaves an edited one as typed.
 */
function DetailsForm({ org }: { org: Organization }) {
  const t = useT();
  const update = useUpdateOrg(org.id);
  const [error, setError] = useState<string | null>(null);
  const form = useForm<UpdateOrgForm>({
    resolver: zodResolver(updateOrgSchema),
    defaultValues: valuesOf(org),
  });
  const { isDirty } = form.formState;
  const { name, slug } = org;
  const seen = useRef({ name, slug });

  useEffect(() => {
    if (seen.current.name === name && seen.current.slug === slug) return;
    seen.current = { name, slug };
    form.reset({ name, slug }, { keepDirtyValues: true });
  }, [form, name, slug]);

  const onSubmit = form.handleSubmit(async (values) => {
    setError(null);
    const next = updateOrgSchema.parse(values);
    const changes: UpdateOrganizationRequest = {};
    if (next.name !== org.name) changes.name = next.name;
    if (next.slug !== org.slug) changes.slug = next.slug;
    if (Object.keys(changes).length === 0) {
      form.reset(valuesOf(org));
      return;
    }
    try {
      const saved = await update.mutateAsync(changes);
      form.reset(valuesOf(saved));
      toast.success(t("orgs.saved"));
    } catch (err) {
      if (isApiError(err) && err.status === 409) {
        form.setError("slug", { type: "server", message: t("orgs.slugTaken") });
        return;
      }
      if (applyFieldErrors(err, form.setError, ["name", "slug"])) return;
      if (isApiError(err) && err.status === 403)
        setError(t("orgs.saveForbidden", { org: org.name }));
      else setError(isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic"));
    }
  });

  return (
    <Form {...form}>
      <form onSubmit={onSubmit} noValidate className="min-w-0 max-w-xl space-y-4">
        <FormAlert>{error}</FormAlert>
        <TextField control={form.control} name="name" label={t("orgs.name")} autoComplete="off" />
        <TextField
          control={form.control}
          name="slug"
          label={t("orgs.slug")}
          description={t("orgs.slugHint")}
          autoComplete="off"
        />
        <Button type="submit" disabled={!isDirty || update.isPending}>
          {t("orgs.save")}
        </Button>
      </form>
    </Form>
  );
}

/** Leaving (OMEM-05): anyone may, except the only admin, who is told to hand the role over first. */
function LeaveSection({ org }: { org: Organization }) {
  const t = useT();
  const id = useId();
  const navigate = useNavigate();
  const me = useMe().data;
  const leave = useRemoveFromOrg();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const close = (next: boolean) => {
    setOpen(next);
    if (!next) setError(null);
  };

  return (
    <Section
      id={`${id}-leave`}
      title={t("orgs.leave")}
      description={t("orgs.leaveBody", { org: org.name })}
    >
      <Button
        variant="outline"
        className="h-auto max-w-full py-2 text-left whitespace-normal"
        onClick={() => setOpen(true)}
      >
        {t("orgs.leaveOpen", { org: org.name })}
      </Button>
      <ConfirmDialog
        open={open}
        onClose={() => close(false)}
        title={t("orgs.leaveTitle", { org: org.name })}
        body={t("orgs.leaveDialogBody", { org: org.name })}
        confirmLabel={t("orgs.leaveConfirm")}
        pending={leave.isPending || !me}
        onConfirm={() => {
          // Stay open while the request runs: a failure must leave the dialog to retry from.
          if (!me) return;
          setError(null);
          leave.mutate(
            { orgId: org.id, iid: me.intelligence.id },
            {
              onSuccess: () => {
                toast(t("orgs.left", { org: org.name }));
                void navigate({ to: "/orgs" });
              },
              onError: (err) => {
                if (isApiError(err) && err.code === ErrorCodeLastAdmin) {
                  setError(t("orgs.leaveLastAdmin", { org: org.name }));
                } else if (isApiError(err) && err.status === 0) {
                  setError(t("error.network"));
                } else {
                  setError(t("orgs.leaveFailed", { org: org.name }));
                }
              },
            },
          );
        }}
      >
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
      </ConfirmDialog>
    </Section>
  );
}

/** Deleting (ORG-06): only an organization that owns nothing; the refusal says what it still owns. */
function DeleteSection({ org }: { org: Organization }) {
  const { t, locale } = useI18n();
  const id = useId();
  const navigate = useNavigate();
  const remove = useDeleteOrg();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const close = (next: boolean) => {
    setOpen(next);
    if (!next) setError(null);
  };

  const refusal = (err: unknown): string => {
    if (isApiError(err) && err.code === ErrorCodeOrganizationNotEmpty) {
      const things = ownedThings(t, locale, err.details);
      return things
        ? t("orgs.notEmpty", { org: org.name, things })
        : t("orgs.notEmptyUnknown", { org: org.name });
    }
    if (isApiError(err) && err.status === 403) return t("orgs.deleteForbidden", { org: org.name });
    if (isApiError(err) && err.status === 0) return t("error.network");
    return t("orgs.deleteFailed", { org: org.name });
  };

  return (
    <Section id={`${id}-delete`} title={t("orgs.delete")} description={t("orgs.deleteBody")}>
      <Button
        variant="outline"
        className="text-destructive hover:text-destructive"
        onClick={() => setOpen(true)}
      >
        {t("orgs.deleteOpen")}
      </Button>
      <ConfirmDialog
        open={open}
        onClose={() => close(false)}
        title={t("orgs.deleteTitle", { org: org.name })}
        body={t("orgs.deleteDialogBody", { org: org.name })}
        confirmLabel={t("orgs.deleteConfirm")}
        pending={remove.isPending}
        onConfirm={() => {
          setError(null);
          remove.mutate(org.id, {
            onSuccess: () => {
              toast(t("orgs.deleted", { org: org.name }));
              void navigate({ to: "/orgs" });
            },
            onError: (err) => setError(refusal(err)),
          });
        }}
      >
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
      </ConfirmDialog>
    </Section>
  );
}
