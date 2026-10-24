import { createFileRoute } from "@tanstack/react-router";
import { MoveRightIcon, Trash2Icon, UserMinusIcon, UserPlusIcon } from "lucide-react";
import { useId, useMemo, useState } from "react";
import { toast } from "sonner";
import { DeleteAIDialog } from "@/components/delete-ai-dialog";
import { IntelligenceAvatar } from "@/components/intelligence/intelligence-avatar";
import { IntelligenceName } from "@/components/intelligence/intelligence-name";
import { InviteDialog } from "@/components/invite-dialog";
import { MoveMemberDialog, type MoveTarget } from "@/components/move-member-dialog";
import { NativeSelect } from "@/components/native-select";
import { PageHeader } from "@/components/page-header";
import { RemoveFromOrgDialog } from "@/components/remove-from-org-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { isApiError } from "@/lib/api/client";
import {
  useMe,
  useOrg,
  useOrgChart,
  useOrgInvitations,
  useOrgMembers,
  useRevokeInvitation,
  useUpdateOrgMember,
} from "@/lib/api/queries";
import {
  DeleteEffectDelete,
  ErrorCodeLastAdmin,
  type Invitation,
  type OrgMember,
  type Permission,
} from "@/lib/api/types.gen";
import { useI18n, useT } from "@/lib/i18n";
import { PermissionAdmin, PermissionGuest, PermissionMember, permissionLabel } from "@/lib/orgs";
import { formatAbsolute, formatRelative } from "@/lib/time";

export const Route = createFileRoute("/_app/orgs/$orgId/members")({
  component: OrgMembersPage,
});

/*
 * Who belongs to the organization (OMEM-03): each member with what they are in it, the position
 * they hold and, for a guest, who answers for them. Anyone who belongs may invite (guests; an admin
 * gives any permission and a position, OMEM-01, OMEM-04); what the viewer may do about each member
 * is what the server's chart says (CHART-03): move between positions (POS-04), and delete an AI of
 * this organization or remove anyone else from it (POS-05, POS-06). Admins also change permissions
 * (OMEM-05) and see the invitations still waiting.
 */

const RANK: Record<string, number> = {
  [PermissionAdmin]: 0,
  [PermissionMember]: 1,
  [PermissionGuest]: 2,
};

/** Admins first, then members, then guests; by name inside each. */
function inOrder(members: readonly OrgMember[]): OrgMember[] {
  return [...members].sort(
    (a, b) =>
      (RANK[a.permission] ?? 3) - (RANK[b.permission] ?? 3) ||
      a.intelligence.display_name.localeCompare(b.intelligence.display_name, undefined, {
        sensitivity: "base",
      }) ||
      (a.intelligence.id < b.intelligence.id ? -1 : 1),
  );
}

function OrgMembersPage() {
  const t = useT();
  const { orgId } = Route.useParams();
  // The layout has loaded the organization and shows its failures.
  const org = useOrg(orgId).data;
  const members = useOrgMembers(orgId);
  const chart = useOrgChart(orgId);
  const me = useMe().data;
  const [inviting, setInviting] = useState(false);
  const [moving, setMoving] = useState<MoveTarget | null>(null);
  const [removing, setRemoving] = useState<OrgMember | null>(null);
  const [deleting, setDeleting] = useState<OrgMember | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const admin = org?.permission === PermissionAdmin;
  const positions = useMemo(
    () => (chart.data?.positions ?? []).map(({ id, name, parent_id }) => ({ id, name, parent_id })),
    [chart.data],
  );
  if (!org) return null;

  return (
    <>
      <PageHeader
        title={t("orgs.membersTitle")}
        description={t("orgs.membersBody", { org: org.name })}
        actions={
          <Button onClick={() => setInviting(true)}>
            <UserPlusIcon aria-hidden="true" />
            {t("orgs.invite")}
          </Button>
        }
      />
      {notice ? (
        <p
          role="alert"
          className="border-b bg-destructive/8 px-4 py-2.5 text-sm text-destructive md:px-8"
        >
          {notice}
        </p>
      ) : null}
      {members.isPending ? (
        <div aria-hidden="true" className="space-y-3 px-4 py-6 md:px-8">
          <Skeleton className="h-5 w-56" />
          <Skeleton className="h-5 w-72 max-w-full" />
          <Skeleton className="h-5 w-48" />
        </div>
      ) : members.isError && !members.data ? (
        <div className="px-4 py-10 text-sm md:px-8" role="alert">
          <p className="text-destructive">{t("orgs.membersError")}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => members.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : (
        <MembersTable
          orgId={org.id}
          orgName={org.name}
          members={inOrder(members.data ?? [])}
          admin={admin}
          viewerId={me?.intelligence.id}
          onNotice={setNotice}
          onMove={(m) =>
            setMoving({
              id: m.intelligence.id,
              name: m.intelligence.display_name,
              positionId: m.position_id ?? "",
            })
          }
          onRemove={setRemoving}
          onDelete={setDeleting}
        />
      )}
      {admin ? <PendingInvitations orgId={org.id} orgName={org.name} /> : null}

      <InviteDialog
        org={{ id: org.id, name: org.name }}
        admin={admin}
        positions={positions}
        members={members.data ?? []}
        open={inviting}
        onOpenChange={setInviting}
      />
      <MoveMemberDialog
        orgId={org.id}
        orgName={org.name}
        target={moving}
        positions={positions}
        viewer={{ admin, ...(org.position_id ? { positionId: org.position_id } : {}) }}
        onClose={() => setMoving(null)}
      />
      <RemoveFromOrgDialog
        identity={
          removing?.intelligence ?? {
            id: "",
            kind: "human",
            role: "user",
            display_name: "",
            handle: "",
          }
        }
        membership={removing ? { org_id: org.id, org_name: org.name } : null}
        onClose={() => setRemoving(null)}
      />
      <DeleteAIDialog ai={deleting?.intelligence ?? null} onClose={() => setDeleting(null)} />
    </>
  );
}

function MembersTable({
  orgId,
  orgName,
  members,
  admin,
  viewerId,
  onNotice,
  onMove,
  onRemove,
  onDelete,
}: {
  orgId: string;
  orgName: string;
  members: OrgMember[];
  admin: boolean;
  viewerId: string | undefined;
  onNotice: (message: string | null) => void;
  onMove: (m: OrgMember) => void;
  onRemove: (m: OrgMember) => void;
  onDelete: (m: OrgMember) => void;
}) {
  const t = useT();
  const prefix = useId();
  return (
    <table
      aria-label={t("orgs.membersLabel", { org: orgName })}
      className="w-full text-sm max-xl:block"
    >
      <thead className="max-xl:sr-only">
        <tr className="border-b text-left text-xs text-muted-foreground">
          <th scope="col" className="px-4 py-2 font-medium xl:px-8">
            {t("orgs.colMember")}
          </th>
          <th scope="col" className="px-3 py-2 font-medium">
            {t("orgs.colPermission")}
          </th>
          <th scope="col" className="px-3 py-2 font-medium">
            {t("orgs.colPosition")}
          </th>
          <th scope="col" className="px-3 py-2 font-medium whitespace-nowrap">
            {t("orgs.colInvitedBy")}
          </th>
          <th scope="col" className="px-4 py-2 text-right font-medium xl:px-8">
            {t("orgs.colActions")}
          </th>
        </tr>
      </thead>
      <tbody className="max-xl:block">
        {members.map((m) => (
          <MemberRow
            key={m.intelligence.id}
            nameId={`${prefix}-${m.intelligence.id}`}
            orgId={orgId}
            orgName={orgName}
            member={m}
            admin={admin}
            viewerId={viewerId}
            onNotice={onNotice}
            onMove={onMove}
            onRemove={onRemove}
            onDelete={onDelete}
          />
        ))}
      </tbody>
    </table>
  );
}

function MemberRow({
  nameId,
  orgId,
  orgName,
  member: m,
  admin,
  viewerId,
  onNotice,
  onMove,
  onRemove,
  onDelete,
}: {
  nameId: string;
  orgId: string;
  orgName: string;
  member: OrgMember;
  admin: boolean;
  viewerId: string | undefined;
  onNotice: (message: string | null) => void;
  onMove: (m: OrgMember) => void;
  onRemove: (m: OrgMember) => void;
  onDelete: (m: OrgMember) => void;
}) {
  const { t, locale } = useI18n();
  const who = m.intelligence;
  const update = useUpdateOrgMember(orgId);

  const setPermission = (permission: Permission) => {
    onNotice(null);
    update.mutate(
      { iid: who.id, permission },
      {
        onSuccess: () =>
          toast(
            t("orgs.permissionSet", {
              name: who.display_name,
              role: permissionLabel(t, permission).toLocaleLowerCase(locale),
            }),
          ),
        onError: (err) => {
          if (isApiError(err) && err.code === ErrorCodeLastAdmin) {
            onNotice(t("profile.removeLastAdmin", { name: who.display_name, org: orgName }));
          } else if (isApiError(err) && err.status === 403) {
            onNotice(t("orgs.permissionForbidden", { org: orgName }));
          } else if (isApiError(err) && err.status === 0) {
            onNotice(t("error.network"));
          } else {
            onNotice(t("orgs.permissionFailed", { name: who.display_name }));
          }
        },
      },
    );
  };

  return (
    <tr className="border-b max-xl:grid max-xl:grid-cols-[auto_minmax(0,1fr)] max-xl:gap-x-3 max-xl:gap-y-2 max-xl:px-4 max-xl:py-3 md:max-xl:px-8">
      <th scope="row" className="min-w-0 text-left font-normal max-xl:col-span-2 xl:px-8 xl:py-3">
        <span id={nameId} className="flex min-w-0 items-center gap-3">
          <IntelligenceAvatar intelligence={who} decorative />
          <IntelligenceName intelligence={who} viewerId={viewerId} showHandle profileLink />
        </span>
      </th>
      <td className="xl:px-3 xl:py-3">
        {admin ? (
          <NativeSelect
            aria-label={t("orgs.permissionOf", { name: who.display_name })}
            value={m.permission}
            disabled={update.isPending}
            onChange={(e) => setPermission(e.target.value as Permission)}
            className="h-8 w-32"
          >
            {[PermissionAdmin, PermissionMember, PermissionGuest].map((p) => (
              <option key={p} value={p}>
                {permissionLabel(t, p)}
              </option>
            ))}
          </NativeSelect>
        ) : (
          <Badge variant="secondary" className="px-1.5 py-0 text-[0.68rem] leading-5">
            {permissionLabel(t, m.permission)}
          </Badge>
        )}
      </td>
      <td className="min-w-0 xl:px-3 xl:py-3">
        {m.position_name ? (
          <span className="break-words">{m.position_name}</span>
        ) : (
          <>
            <span className="sr-only">{t("orgs.noPosition")}</span>
            <span aria-hidden="true" className="text-muted-foreground">
              —
            </span>
          </>
        )}
      </td>
      <td className="min-w-0 text-muted-foreground max-xl:col-span-2 xl:px-3 xl:py-3">
        {m.invited_by ? t("orgs.invitedBy", { name: m.invited_by.display_name }) : null}
      </td>
      <td className="max-xl:col-span-2 xl:px-8 xl:py-3">
        <div className="flex flex-wrap gap-2 xl:justify-end">
          {m.can_move ? (
            <Button variant="outline" size="sm" aria-describedby={nameId} onClick={() => onMove(m)}>
              <MoveRightIcon aria-hidden="true" />
              {t("orgs.move")}
            </Button>
          ) : null}
          {m.can_delete && m.delete_effect === DeleteEffectDelete ? (
            <Button
              variant="outline"
              size="sm"
              className="text-destructive hover:text-destructive"
              aria-describedby={nameId}
              onClick={() => onDelete(m)}
            >
              <Trash2Icon aria-hidden="true" />
              {t("orgs.deleteMember")}
            </Button>
          ) : null}
          {m.can_delete && m.delete_effect !== DeleteEffectDelete ? (
            <Button
              variant="outline"
              size="sm"
              className="text-destructive hover:text-destructive"
              aria-describedby={nameId}
              onClick={() => onRemove(m)}
            >
              <UserMinusIcon aria-hidden="true" />
              {t("profile.removeFrom", { org: orgName })}
            </Button>
          ) : null}
        </div>
      </td>
    </tr>
  );
}

/** The invitations still waiting for an answer, which an admin may withdraw (OMEM-01, OMEM-07). */
function PendingInvitations({ orgId, orgName }: { orgId: string; orgName: string }) {
  const t = useT();
  const id = useId();
  const pending = useOrgInvitations(orgId);
  const waiting = pending.data ?? [];
  if (waiting.length === 0) return null;
  return (
    <section aria-labelledby={`${id}-title`} className="border-b">
      <div className="px-4 pt-5 md:px-8">
        <h2 id={`${id}-title`} className="text-lg font-semibold tracking-[-0.015em]">
          {t("orgs.pending")}
        </h2>
        <p className="text-sm text-muted-foreground">{t("orgs.pendingBody")}</p>
      </div>
      <ul aria-label={t("orgs.pendingLabel", { org: orgName })} className="divide-y">
        {waiting.map((inv) => (
          <PendingItem key={inv.id} orgId={orgId} invitation={inv} />
        ))}
      </ul>
    </section>
  );
}

function PendingItem({ orgId, invitation: inv }: { orgId: string; invitation: Invitation }) {
  const { t, locale } = useI18n();
  const nameId = useId();
  const revoke = useRevokeInvitation(orgId);
  const name = inv.invitee.display_name;
  return (
    <li aria-label={name} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 md:px-8">
      <span id={nameId} className="flex min-w-0 flex-1 items-center gap-3">
        <IntelligenceAvatar intelligence={inv.invitee} decorative />
        <IntelligenceName intelligence={inv.invitee} showHandle />
      </span>
      <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
        <Badge variant="secondary" className="px-1.5 py-0 text-[0.68rem] leading-5">
          {permissionLabel(t, inv.permission)}
        </Badge>
        {inv.position_name ? <span>{inv.position_name}</span> : null}
        <span>{t("orgs.invitedBy", { name: inv.invited_by.display_name })}</span>
        <time dateTime={inv.expires_at} title={formatAbsolute(inv.expires_at, locale)}>
          {t("orgs.expires", { when: formatRelative(inv.expires_at, locale) })}
        </time>
      </span>
      <Button
        variant="outline"
        size="sm"
        aria-describedby={nameId}
        disabled={revoke.isPending}
        onClick={() =>
          revoke.mutate(inv.id, {
            onSuccess: () => toast(t("orgs.revoked", { name })),
            onError: () => toast.error(t("orgs.revokeFailed", { name })),
          })
        }
      >
        {t("orgs.revoke")}
      </Button>
    </li>
  );
}
