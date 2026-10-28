import { zodResolver } from "@hookform/resolvers/zod";
import { useMemo, useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { IntelligenceAvatar, IntelligenceName } from "@/entities/intelligence";
import { useContacts, useInviteToOrg } from "@/lib/api/queries";
import { inviteCandidates } from "@/lib/contacts";
import { positionOptions } from "@/lib/org-chart";
import { PermissionAdmin, PermissionGuest, PermissionMember, permissionLabel } from "@/lib/orgs";
import { type InviteFormValues, inviteSchema } from "@/lib/schemas";
import { isApiError } from "@/shared/api/client";
import {
  ErrorCodeAlreadyMember,
  ErrorCodeContactNotFound,
  type OrgMember,
  type Permission,
  type Position,
} from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { applyFieldErrors } from "@/shared/lib/forms";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/shared/ui/molecules/dialog";
import { FormAlert, SelectField, TextField } from "@/shared/ui/molecules/form-fields";
import { Button } from "@/shared/ui/primitives/button";
import { Form } from "@/shared/ui/primitives/form";

/**
 * Invites someone to the organization by @handle (OMEM-01). An admin gives any permission and
 * a position; every other member, a guest included, invites guests and nothing more, so those are
 * the only things the dialog has to say and send (OMEM-04). What is typed suggests the users and
 * AIs of the instance that do not belong yet; the server says in the end whether the handle is a
 * user or an AI at all, and whether they already belong.
 */
export function InviteDialog({
  org,
  admin,
  positions,
  members,
  open,
  onOpenChange,
}: {
  org: { id: string; name: string };
  admin: boolean;
  positions: readonly Position[];
  members: readonly OrgMember[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const t = useT();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("orgs.inviteTitle", { org: org.name })}</DialogTitle>
          <DialogDescription>
            {admin ? t("orgs.inviteBodyAdmin") : t("orgs.inviteBodyGuest", { org: org.name })}
          </DialogDescription>
        </DialogHeader>
        {/* Mounted only while open: every opening starts with an empty form. */}
        {open ? (
          <InviteForm
            org={org}
            admin={admin}
            positions={positions}
            members={members}
            onDone={() => onOpenChange(false)}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function InviteForm({
  org,
  admin,
  positions,
  members,
  onDone,
}: {
  org: { id: string; name: string };
  admin: boolean;
  positions: readonly Position[];
  members: readonly OrgMember[];
  onDone: () => void;
}) {
  const t = useT();
  const invite = useInviteToOrg(org.id);
  const contacts = useContacts();
  const [error, setError] = useState<string | null>(null);
  const form = useForm<InviteFormValues>({
    resolver: zodResolver(inviteSchema),
    defaultValues: { handle: "", permission: PermissionMember, position_id: "" },
  });
  const typed = form.watch("handle");
  const memberIds = useMemo(() => new Set(members.map((m) => m.intelligence.id)), [members]);
  const suggestions = useMemo(
    () => (contacts.data ? inviteCandidates(contacts.data, memberIds, typed) : []),
    [contacts.data, memberIds, typed],
  );
  const tree = useMemo(() => positionOptions(positions), [positions]);

  const onSubmit = form.handleSubmit((values) => {
    if (invite.isPending) return;
    setError(null);
    const next = inviteSchema.parse(values);
    invite.mutate(
      admin
        ? {
            handle: next.handle,
            permission: next.permission,
            ...(next.position_id ? { position_id: next.position_id } : {}),
          }
        : { handle: next.handle, permission: PermissionGuest },
      {
        onSuccess: (sent) => {
          toast.success(t("orgs.invited", { name: sent.invitee.display_name }));
          onDone();
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
          } else if (
            applyFieldErrors(err, form.setError, ["handle", "permission", "position_id"])
          ) {
            // Shown under the fields.
          } else {
            setError(isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic"));
          }
        },
      },
    );
  });

  return (
    <Form {...form}>
      <form onSubmit={onSubmit} noValidate className="space-y-4">
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
        {admin ? (
          <>
            <SelectField
              control={form.control}
              name="permission"
              label={t("orgs.invitePermission")}
            >
              {([PermissionAdmin, PermissionMember, PermissionGuest] as Permission[]).map((p) => (
                <option key={p} value={p}>
                  {permissionLabel(t, p)}
                </option>
              ))}
            </SelectField>
            <SelectField
              control={form.control}
              name="position_id"
              label={t("orgs.invitePositionLabel")}
            >
              <option value="">{t("orgs.noPosition")}</option>
              {tree.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </SelectField>
          </>
        ) : null}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onDone}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" disabled={invite.isPending}>
            {invite.isPending ? t("orgs.inviteSending") : t("orgs.inviteSend")}
          </Button>
        </DialogFooter>
      </form>
    </Form>
  );
}
