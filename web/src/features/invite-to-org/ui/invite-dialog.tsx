import { zodResolver } from "@hookform/resolvers/zod";
import { useMemo, useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import {
  IntelligenceAvatar,
  IntelligenceName,
  inviteCandidates,
  useContacts,
} from "@/entities/intelligence";
import {
  PermissionAdmin,
  PermissionMember,
  permissionLabel,
  useInviteToOrg,
} from "@/entities/organization";
import { isApiError } from "@/shared/api/client";
import {
  ErrorCodeAlreadyMember,
  ErrorCodeContactNotFound,
  type OrgMember,
  type Permission,
} from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { applyFieldErrors } from "@/shared/lib/forms";
import { FormDialog } from "@/shared/ui/molecules/form-dialog";
import { FormAlert, SelectField, TextField } from "@/shared/ui/molecules/form-fields";
import { Form } from "@/shared/ui/primitives/form";
import { type InviteFormValues, inviteSchema } from "../model/schema";

/**
 * Invites someone to the organization by @handle, as an admin or a member (OMEM-01, SR2-01). Only an
 * admin invites (OMEM-04), so only an admin's page offers the dialog. What is typed suggests the
 * users and AIs of the instance that do not belong yet; the server says in the end whether the handle
 * is a user or an AI at all, and whether they already belong.
 */
export function InviteDialog({
  org,
  members,
  open,
  onOpenChange,
}: {
  org: { id: string; name: string };
  members: readonly OrgMember[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  // Mounted only while open: every opening starts with an empty form.
  return open ? <InviteForm org={org} members={members} onOpenChange={onOpenChange} /> : null;
}

function InviteForm({
  org,
  members,
  onOpenChange,
}: {
  org: { id: string; name: string };
  members: readonly OrgMember[];
  onOpenChange: (open: boolean) => void;
}) {
  const t = useT();
  const invite = useInviteToOrg(org.id);
  const contacts = useContacts();
  const [error, setError] = useState<string | null>(null);
  const form = useForm<InviteFormValues>({
    resolver: zodResolver(inviteSchema),
    defaultValues: { handle: "", permission: PermissionMember },
  });
  const typed = form.watch("handle");
  const memberIds = useMemo(() => new Set(members.map((m) => m.intelligence.id)), [members]);
  const suggestions = useMemo(
    () => (contacts.data ? inviteCandidates(contacts.data, memberIds, typed) : []),
    [contacts.data, memberIds, typed],
  );

  const onSubmit = form.handleSubmit((values) => {
    if (invite.isPending) return;
    setError(null);
    const next = inviteSchema.parse(values);
    invite.mutate(
      { handle: next.handle, permission: next.permission },
      {
        onSuccess: (sent) => {
          toast.success(t("orgs.invited", { name: sent.invitee.display_name }));
          onOpenChange(false);
        },
        onError: (err) => {
          if (isApiError(err) && err.code === ErrorCodeContactNotFound) {
            form.setError("handle", { type: "server", message: t("orgs.inviteUnknown") });
          } else if (isApiError(err) && err.code === ErrorCodeAlreadyMember) {
            form.setError("handle", {
              type: "server",
              message: t("orgs.inviteAlready", { handle: `@${next.handle}`, org: org.name }),
            });
          } else if (isApiError(err) && err.status === 429) {
            setError(t("orgs.inviteRate"));
          } else if (isApiError(err) && err.status === 403) {
            setError(t("orgs.inviteForbidden"));
          } else if (applyFieldErrors(err, form.setError, ["handle", "permission"])) {
            // Shown under the fields.
          } else {
            setError(isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic"));
          }
        },
      },
    );
  });

  return (
    <FormDialog
      open
      onOpenChange={onOpenChange}
      title={t("orgs.inviteTitle", { org: org.name })}
      description={t("orgs.inviteBodyAdmin")}
      onSubmit={onSubmit}
      contentClassName="max-h-[calc(100dvh-2rem)] overflow-y-auto"
      submitLabel={invite.isPending ? t("orgs.inviteSending") : t("orgs.inviteSend")}
      pending={invite.isPending}
    >
      <Form {...form}>
        <FormAlert>{error}</FormAlert>
        <div className="space-y-2">
          <TextField
            control={form.control}
            name="handle"
            label={t("orgs.inviteHandle")}
            description={t("orgs.inviteHandleHint")}
            autoComplete="off"
            autoFocus
          />
          {suggestions.length > 0 ? (
            <ul aria-label={t("orgs.inviteSuggestions")} className="-mx-1 space-y-0.5">
              {suggestions.map((p) => (
                <li key={p.ref.id}>
                  <button
                    type="button"
                    onClick={() =>
                      form.setValue("handle", `@${p.ref.handle}`, { shouldDirty: true })
                    }
                    className="flex w-full min-w-0 items-center gap-2 rounded-md px-1 py-1.5 text-left outline-none hover:bg-accent/60 focus-visible:bg-accent focus-visible:ring-2 focus-visible:ring-ring/70"
                  >
                    <IntelligenceAvatar intelligence={p.ref} size="sm" decorative />
                    <IntelligenceName intelligence={p.ref} showHandle className="text-sm" />
                  </button>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
        <SelectField control={form.control} name="permission" label={t("orgs.invitePermission")}>
          {([PermissionAdmin, PermissionMember] as Permission[]).map((p) => (
            <option key={p} value={p}>
              {permissionLabel(t, p)}
            </option>
          ))}
        </SelectField>
      </Form>
    </FormDialog>
  );
}
