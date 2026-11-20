import { getRouteApi } from "@tanstack/react-router";
import { UserPlusIcon } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";
import { useMe } from "@/entities/account";
import { IntelligenceAvatar, IntelligenceName } from "@/entities/intelligence";
import {
  isAdminOf,
  PermissionAdmin,
  PermissionMember,
  permissionLabel,
  useOrg,
  useOrgInvitations,
  useOrgMembers,
  useRevokeInvitation,
  useUpdateOrgMember,
} from "@/entities/organization";
import { InviteDialog } from "@/features/invite-to-org";
import {
  DeleteAIDialog,
  OrgMemberActions,
  RemoveFromOrgDialog,
} from "@/features/manage-org-member";
import { isApiError } from "@/shared/api/client";
import {
  ErrorCodeLastAdmin,
  type Invitation,
  type OrgMember,
  type Permission,
} from "@/shared/api/types.gen";
import { useI18n, useT } from "@/shared/i18n";
import { RelativeTime } from "@/shared/ui/atoms/relative-time";
import { SelectControl } from "@/shared/ui/molecules/form-fields";
import { PageHeader } from "@/shared/ui/molecules/page-header";
import { Badge } from "@/shared/ui/primitives/badge";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";

const routeApi = getRouteApi("/_app/orgs/$orgId/members");

/*
 * Who belongs to the organization (OMEM-03): each member with its permission. Only an admin invites,
 * as an admin or a member (OMEM-01, OMEM-04, SR2-01); what the viewer may do about each member is
 * what the server says: delete an AI of this organization (its host or an admin) or remove anyone
 * else from it (an admin) (SR2-12, POS-06). Admins also change permissions (OMEM-05) and see the
 * invitations still waiting.
 */

const RANK: Record<string, number> = {
  [PermissionAdmin]: 0,
  [PermissionMember]: 1,
};

/** Admins first, then members; by name inside each. */
function inOrder(members: readonly OrgMember[]): OrgMember[] {
  return [...members].sort(
    (a, b) =>
      (RANK[a.permission] ?? 2) - (RANK[b.permission] ?? 2) ||
      a.intelligence.display_name.localeCompare(b.intelligence.display_name, undefined, {
        sensitivity: "base",
      }) ||
      (a.intelligence.id < b.intelligence.id ? -1 : 1),
  );
}

export function OrgMembersPage() {
  const t = useT();
  const { orgId } = routeApi.useParams();
  // The layout has loaded the organization and shows its failures.
  const org = useOrg(orgId).data;
  const members = useOrgMembers(orgId);
  const me = useMe().data;
  const [inviting, setInviting] = useState(false);
  const [removing, setRemoving] = useState<OrgMember | null>(null);
  const [deleting, setDeleting] = useState<OrgMember | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const admin = !!org && isAdminOf(org);
  if (!org) return null;

  return (
    <>
      <PageHeader
        title={t("orgs.membersTitle")}
        description={t("orgs.membersBody", { org: org.name })}
        actions={
          admin ? (
            <Button onClick={() => setInviting(true)}>
              <UserPlusIcon aria-hidden="true" />
              {t("orgs.invite")}
            </Button>
          ) : undefined
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
          onRemove={setRemoving}
          onDelete={setDeleting}
        />
      )}
      {admin ? <PendingInvitations orgId={org.id} orgName={org.name} /> : null}

      {admin ? (
        <InviteDialog
          org={{ id: org.id, name: org.name }}
          members={members.data ?? []}
          open={inviting}
          onOpenChange={setInviting}
        />
      ) : null}
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
  onRemove,
  onDelete,
}: {
  orgId: string;
  orgName: string;
  members: OrgMember[];
  admin: boolean;
  viewerId: string | undefined;
  onNotice: (message: string | null) => void;
  onRemove: (m: OrgMember) => void;
  onDelete: (m: OrgMember) => void;
}) {
  const t = useT();
  const prefix = useId();
  return (
    <table
      aria-label={t("orgs.membersLabel", { org: orgName })}
      className="w-full text-sm max-xl:block xl:table-fixed"
    >
      {/* Fixed columns: a name with no place to break wraps or is cut inside its column, and never
          widens the page. The widths add up to 100%. */}
      <thead className="max-xl:sr-only">
        <tr className="border-b text-left text-xs text-muted-foreground">
          <th scope="col" className="px-4 py-2 font-medium xl:w-[50%] xl:px-8">
            {t("orgs.colMember")}
          </th>
          <th scope="col" className="px-3 py-2 font-medium xl:w-[20%]">
            {t("orgs.colPermission")}
          </th>
          <th scope="col" className="px-4 py-2 text-right font-medium xl:w-[30%] xl:px-8">
            {t("orgs.colActions")}
          </th>
        </tr>
      </thead>
      <tbody className="max-xl:block">
        {members.map((m) => (
          <OrgMemberRow
            key={m.intelligence.id}
            nameId={`${prefix}-${m.intelligence.id}`}
            orgId={orgId}
            orgName={orgName}
            member={m}
            admin={admin}
            viewerId={viewerId}
            onNotice={onNotice}
            onRemove={onRemove}
            onDelete={onDelete}
          />
        ))}
      </tbody>
    </table>
  );
}

function OrgMemberRow({
  nameId,
  orgId,
  orgName,
  member: m,
  admin,
  viewerId,
  onNotice,
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
          <SelectControl
            label={t("orgs.permissionOf", { name: who.display_name })}
            hideLabel
            value={m.permission}
            disabled={update.isPending}
            onChange={(e) => setPermission(e.target.value as Permission)}
            className="w-32"
            selectClassName="h-8"
          >
            {[PermissionAdmin, PermissionMember].map((p) => (
              <option key={p} value={p}>
                {permissionLabel(t, p)}
              </option>
            ))}
          </SelectControl>
        ) : (
          <Badge variant="secondary" className="px-1.5 py-0 text-[0.68rem] leading-5">
            {permissionLabel(t, m.permission)}
          </Badge>
        )}
      </td>
      <td className="max-xl:col-span-2 xl:px-8 xl:py-3">
        <OrgMemberActions
          member={m}
          orgName={orgName}
          describedBy={nameId}
          onRemove={onRemove}
          onDelete={onDelete}
        />
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
        <span>{t("orgs.invitedBy", { name: inv.invited_by.display_name })}</span>
        <RelativeTime value={inv.expires_at} locale={locale}>
          {(when) => t("orgs.expires", { when })}
        </RelativeTime>
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
