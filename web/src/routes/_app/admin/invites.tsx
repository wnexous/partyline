import { createFileRoute } from "@tanstack/react-router";
import { MailPlusIcon, PlusIcon } from "lucide-react";
import { useId, useMemo, useState } from "react";
import { toast } from "sonner";
import { SecretOnceDialog } from "@/components/secret-once-dialog";
import { useAdminInvites, useCreateInvite, useRevokeInvite } from "@/lib/api/queries";
import {
  type CreatedInvite,
  type Invite,
  type InviteStatus,
  type Role,
  RoleAdmin,
  RoleMember,
} from "@/shared/api/types.gen";
import { type MessageKey, useI18n, useT } from "@/shared/i18n";
import { formatAbsolute } from "@/shared/lib/time";
import { cn } from "@/shared/lib/utils";
import { RelativeTime } from "@/shared/ui/atoms/relative-time";
import { ConfirmDialog } from "@/shared/ui/molecules/confirm-dialog";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/shared/ui/molecules/dialog";
import { EmptyState } from "@/shared/ui/molecules/empty-state";
import { PageHeader } from "@/shared/ui/molecules/page-header";
import { Button } from "@/shared/ui/primitives/button";
import { RadioGroup, RadioGroupItem } from "@/shared/ui/primitives/radio-group";
import { Skeleton } from "@/shared/ui/primitives/skeleton";

export const Route = createFileRoute("/_app/admin/invites")({
  component: AdminInvitesPage,
});

/*
 * Invites (UI-20): single-use links with a role. The link embeds the secret token and comes back
 * only from the create call, so it is shown once (copy it then); the list shows status only.
 */

const STATUS: Record<InviteStatus, { label: MessageKey; tone: string }> = {
  pending: {
    label: "invites.status.pending",
    tone: "bg-tape text-tape-foreground",
  },
  used: {
    label: "invites.status.used",
    tone: "text-foreground ring-1 ring-tape-strong/50 ring-inset",
  },
  revoked: { label: "invites.status.revoked", tone: "bg-muted text-muted-foreground" },
  expired: { label: "invites.status.expired", tone: "bg-muted text-muted-foreground" },
};

const ROLE_LABEL: Record<Role, MessageKey> = {
  admin: "admin.role.admin",
  member: "admin.role.member",
};

function AdminInvitesPage() {
  const t = useT();
  const invites = useAdminInvites();
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<CreatedInvite | null>(null);
  const [revoking, setRevoking] = useState<Invite | null>(null);
  const { locale } = useI18n();
  const list = useMemo(
    () =>
      [...(invites.data ?? [])].sort(
        (a, b) =>
          Number(b.status === "pending") - Number(a.status === "pending") ||
          Date.parse(b.created_at) - Date.parse(a.created_at),
      ),
    [invites.data],
  );

  const create = (
    <Button onClick={() => setCreating(true)}>
      <PlusIcon aria-hidden="true" />
      {t("invites.new")}
    </Button>
  );

  return (
    <>
      <PageHeader
        title={t("page.adminInvitesTitle")}
        description={t("page.adminInvitesBody")}
        actions={list.length > 0 ? create : null}
      />
      {invites.isPending ? (
        <div className="space-y-3 px-4 py-6 md:px-8" aria-hidden="true">
          <Skeleton className="h-4 w-48" />
          <Skeleton className="h-4 w-64 max-w-full" />
        </div>
      ) : invites.isError ? (
        <div className="px-4 py-10 text-sm md:px-8" role="alert">
          <p className="text-destructive">{t("invites.loadError")}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => invites.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : list.length === 0 ? (
        <EmptyState icon={MailPlusIcon} title={t("invites.emptyTitle")} action={create}>
          <p>{t("invites.emptyBody")}</p>
        </EmptyState>
      ) : (
        <ul aria-label={t("invites.list")} className="divide-y border-b">
          {list.map((inv) => (
            <InviteRow key={inv.id} invite={inv} onRevoke={() => setRevoking(inv)} />
          ))}
        </ul>
      )}
      <CreateInviteDialog
        open={creating}
        onOpenChange={setCreating}
        onCreated={(c) => {
          setCreating(false);
          setCreated(c);
        }}
      />
      <SecretOnceDialog
        secret={created?.url ?? null}
        title={t("invites.linkTitle")}
        description={
          created
            ? t("invites.linkBody", {
                role: t(ROLE_LABEL[created.invite.role]).toLowerCase(),
                expires: formatAbsolute(created.invite.expires_at, locale),
              })
            : undefined
        }
        label={t("invites.linkLabel")}
        warning={t("invites.linkWarning")}
        confirmLabel={t("connect.done")}
        onClose={() => setCreated(null)}
      />
      <RevokeInviteDialog invite={revoking} onClose={() => setRevoking(null)} />
    </>
  );
}

function InviteRow({ invite: inv, onRevoke }: { invite: Invite; onRevoke: () => void }) {
  const { t, locale } = useI18n();
  const status = STATUS[inv.status];
  return (
    <li className="flex items-center gap-4 px-4 py-3.5 md:px-8">
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-2">
          <span className="font-semibold tracking-[-0.01em]">
            {t("invites.as", { role: t(ROLE_LABEL[inv.role]) })}
          </span>
          <span
            className={cn("rounded-[3px] px-1.5 py-px text-[0.7rem] font-semibold", status.tone)}
          >
            {t(status.label)}
          </span>
        </p>
        <p className="flex flex-wrap gap-x-3 text-xs text-muted-foreground">
          <RelativeTime value={inv.created_at} locale={locale}>
            {(time) => t("invites.createdBy", { name: `@${inv.created_by.username}`, time })}
          </RelativeTime>
          {inv.status === "used" && inv.used_by ? (
            inv.used_at ? (
              <RelativeTime value={inv.used_at} locale={locale}>
                {(time) => t("invites.usedBy", { name: `@${inv.used_by?.username}`, time })}
              </RelativeTime>
            ) : (
              <span>{t("invites.usedBy", { name: `@${inv.used_by.username}`, time: "" })}</span>
            )
          ) : inv.status === "pending" ? (
            <RelativeTime value={inv.expires_at} locale={locale}>
              {(time) => t("invites.expires", { time })}
            </RelativeTime>
          ) : null}
        </p>
      </div>
      {inv.status === "pending" ? (
        <Button
          variant="outline"
          size="sm"
          className="text-destructive hover:text-destructive"
          aria-label={t("invites.revoke", { role: t(ROLE_LABEL[inv.role]) })}
          onClick={onRevoke}
        >
          {t("tokens.revokeShort")}
        </Button>
      ) : null}
    </li>
  );
}

function CreateInviteDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (created: CreatedInvite) => void;
}) {
  const t = useT();
  const ids = useId();
  const create = useCreateInvite();
  const [role, setRole] = useState<Role>(RoleMember);

  const submit = () =>
    create.mutate(
      { role },
      {
        onSuccess: (c) => {
          setRole(RoleMember);
          onCreated(c);
        },
        onError: () => toast.error(t("invites.createFailed")),
      },
    );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("invites.new")}</DialogTitle>
          <DialogDescription>{t("invites.newBody")}</DialogDescription>
        </DialogHeader>
        <fieldset>
          <legend className="mb-2 text-sm font-medium">{t("invites.role")}</legend>
          <RadioGroup value={role} onValueChange={(v) => setRole(v as Role)} className="gap-2">
            {([RoleMember, RoleAdmin] as Role[]).map((r) => (
              <label
                key={r}
                htmlFor={`${ids}-${r}`}
                className={cn(
                  "flex cursor-pointer items-start gap-3 rounded-md border px-3 py-2.5 transition-colors hover:bg-accent/60",
                  role === r && "border-tape-strong bg-tape/40",
                )}
              >
                <RadioGroupItem id={`${ids}-${r}`} value={r} className="mt-0.5" />
                <span className="space-y-0.5">
                  <span className="block text-sm font-medium">{t(ROLE_LABEL[r])}</span>
                  <span className="block text-xs text-muted-foreground">
                    {t(r === RoleAdmin ? "invites.adminHint" : "invites.memberHint")}
                  </span>
                </span>
              </label>
            ))}
          </RadioGroup>
        </fieldset>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button onClick={submit} disabled={create.isPending}>
            {t("invites.create")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function RevokeInviteDialog({ invite, onClose }: { invite: Invite | null; onClose: () => void }) {
  const t = useT();
  const revoke = useRevokeInvite();
  return (
    <ConfirmDialog
      open={invite !== null}
      onClose={onClose}
      title={t("invites.revokeTitle")}
      body={t("invites.revokeBody")}
      confirmLabel={t("invites.revokeConfirm")}
      pending={revoke.isPending}
      onConfirm={() => {
        if (!invite) return;
        revoke.mutate(invite.id, {
          onSuccess: () => onClose(),
          onError: () => toast.error(t("invites.revokeFailed")),
        });
      }}
    />
  );
}
