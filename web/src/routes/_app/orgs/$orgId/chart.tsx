import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { MoveRightIcon, PencilRulerIcon, Trash2Icon, UserMinusIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { ChartTree, type TreeRowState } from "@/components/chart-tree";
import { DeleteAIDialog } from "@/components/delete-ai-dialog";
import { MoveMemberDialog, type MoveTarget } from "@/components/move-member-dialog";
import { OrgNotFound } from "@/components/org-not-found";
import { PageHeader } from "@/shared/ui/molecules/page-header";
import { RemoveFromOrgDialog } from "@/components/remove-from-org-dialog";
import { StatusDot } from "@/shared/ui/atoms/status-dot";
import { Badge } from "@/shared/ui/primitives/badge";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { IntelligenceAvatar, YouBadge } from "@/entities/intelligence";
import { isApiError } from "@/shared/api/client";
import { useMe, useOrg, useOrgChart } from "@/lib/api/queries";
import {
  type ChartMember,
  DeleteEffectDelete,
  type IntelligenceRef,
  PermissionAdmin,
  PermissionGuest,
} from "@/shared/api/types.gen";
import { type Translate, useT } from "@/shared/i18n";
import { useLiveStore } from "@/lib/live/store";
import { buildChartTree, type ChartTreeNode } from "@/lib/org-chart";

export const Route = createFileRoute("/_app/orgs/$orgId/chart")({
  component: OrgChartPage,
});

/*
 * Who is where (CHART-01 to CHART-04): the positions as a tree, each with the identities that hold
 * it; a guest under whoever invited it, an AI that holds no position under its creator, and the
 * rest under "Unassigned". Every member of the organization, guests included, sees it (CHART-02).
 * What the viewer may do about each one is what the server's flags say (CHART-03): move between
 * positions, delete an AI of this organization, remove anyone else from it; admins can also go to
 * the positions editor. The tree is keyboard and screen-reader accessible (components/chart-tree.tsx).
 */
function OrgChartPage() {
  const t = useT();
  const { orgId } = Route.useParams();
  const navigate = useNavigate();
  // The layout has loaded the organization and shows its failures.
  const org = useOrg(orgId).data;
  const chart = useOrgChart(orgId);
  const me = useMe().data;
  const [moving, setMoving] = useState<MoveTarget | null>(null);
  const [removing, setRemoving] = useState<IntelligenceRef | null>(null);
  const [deleting, setDeleting] = useState<IntelligenceRef | null>(null);

  const data = chart.data;
  const tree = useMemo(() => (data ? buildChartTree(data) : []), [data]);
  // Where each holder sits now, for the move dialog to start on.
  const held = useMemo(
    () =>
      new Map(
        (data?.positions ?? []).flatMap((p) =>
          p.holders.map((h) => [h.intelligence.id, p.id] as const),
        ),
      ),
    [data],
  );
  const positions = useMemo(
    () => (data?.positions ?? []).map(({ id, name, parent_id }) => ({ id, name, parent_id })),
    [data],
  );

  if (!org) return null;
  if (chart.isError && isApiError(chart.error) && chart.error.status === 404) {
    return <OrgNotFound />;
  }
  if (chart.isError && !data) {
    return (
      <div className="px-4 py-10 text-sm md:px-8" role="alert">
        <p className="text-destructive">{t("orgs.chartError")}</p>
        <Button variant="outline" size="sm" className="mt-3" onClick={() => chart.refetch()}>
          {t("common.retry")}
        </Button>
      </div>
    );
  }

  return (
    <>
      <PageHeader
        title={t("orgs.chartTitle")}
        description={t("orgs.chartBody", { org: org.name })}
        actions={
          data?.viewer.admin ? (
            <Button asChild variant="outline">
              <Link to="/orgs/$orgId/positions" params={{ orgId: org.id }}>
                <PencilRulerIcon aria-hidden="true" />
                {t("orgs.editPositions")}
              </Link>
            </Button>
          ) : null
        }
      />
      {chart.isPending ? (
        <div aria-hidden="true" className="space-y-3 px-4 py-6 md:px-8">
          <Skeleton className="h-6 w-40" />
          <Skeleton className="ml-6 h-6 w-56" />
          <Skeleton className="ml-12 h-6 w-48" />
        </div>
      ) : (
        <div className="px-3 py-4 md:px-7">
          <ChartTree
            nodes={tree}
            label={t("orgs.chartLabel", { org: org.name })}
            onOpen={(node) => {
              const who = node.member?.intelligence;
              if (who) void navigate({ to: "/contacts/$id", params: { id: who.id } });
            }}
            row={(node, state) => (
              <ChartRow
                node={node}
                state={state}
                orgName={org.name}
                viewerId={me?.intelligence.id}
                onMove={(m) =>
                  setMoving({
                    id: m.intelligence.id,
                    name: m.intelligence.display_name,
                    positionId: held.get(m.intelligence.id) ?? "",
                  })
                }
                onRemove={(m) => setRemoving(m.intelligence)}
                onDelete={(m) => setDeleting(m.intelligence)}
              />
            )}
          />
        </div>
      )}

      <MoveMemberDialog
        orgId={org.id}
        orgName={org.name}
        target={moving}
        positions={positions}
        viewer={{
          admin: data?.viewer.admin ?? false,
          ...(org.position_id ? { positionId: org.position_id } : {}),
        }}
        onClose={() => setMoving(null)}
      />
      <RemoveFromOrgDialog
        identity={removing ?? { id: "", kind: "human", role: "user", display_name: "", handle: "" }}
        membership={removing ? { org_id: org.id, org_name: org.name } : null}
        onClose={() => setRemoving(null)}
      />
      <DeleteAIDialog ai={deleting} onClose={() => setDeleting(null)} />
    </>
  );
}

function holdersText(t: Translate, count: number): string {
  if (count === 0) return t("orgs.holdersNone");
  return t(count === 1 ? "orgs.holdersOne" : "orgs.holdersMany", { count });
}

function ChartRow({
  node,
  state,
  orgName,
  viewerId,
  onMove,
  onRemove,
  onDelete,
}: {
  node: ChartTreeNode;
  state: TreeRowState;
  orgName: string;
  viewerId: string | undefined;
  onMove: (m: ChartMember) => void;
  onRemove: (m: ChartMember) => void;
  onDelete: (m: ChartMember) => void;
}) {
  const t = useT();
  if (node.member) {
    return (
      <MemberRow
        member={node.member}
        state={state}
        orgName={orgName}
        viewerId={viewerId}
        onMove={onMove}
        onRemove={onRemove}
        onDelete={onDelete}
      />
    );
  }
  if (node.position) {
    return (
      <div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-3 gap-y-0.5 py-0.5">
        <span id={state.labelId} className="min-w-0 font-semibold tracking-[-0.01em] break-words">
          {node.position.name}
        </span>
        <span id={state.descId} className="text-xs text-muted-foreground">
          {holdersText(t, node.position.holders.length)}
        </span>
      </div>
    );
  }
  return (
    <div className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-3 gap-y-0.5 py-0.5">
      <span id={state.labelId} className="font-semibold tracking-[-0.01em]">
        {t("orgs.unassigned")}
      </span>
      <span id={state.descId} className="text-xs text-muted-foreground">
        {t("orgs.unassignedHint")}
      </span>
    </div>
  );
}

function MemberRow({
  member: m,
  state,
  orgName,
  viewerId,
  onMove,
  onRemove,
  onDelete,
}: {
  member: ChartMember;
  state: TreeRowState;
  orgName: string;
  viewerId: string | undefined;
  onMove: (m: ChartMember) => void;
  onRemove: (m: ChartMember) => void;
  onDelete: (m: ChartMember) => void;
}) {
  const t = useT();
  const who = m.intelligence;
  const isAI = who.kind === "ai";
  // The live presence wins over what the chart said when it was fetched.
  const live = useLiveStore((s) => s.presence[who.id]);
  const online = live?.online ?? m.online;
  const tab = state.tabbable ? 0 : -1;
  const name = who.display_name;
  const deletes = m.can_delete && m.delete_effect === DeleteEffectDelete;
  const removes = m.can_delete && m.delete_effect !== DeleteEffectDelete;

  return (
    <div className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2 gap-y-1">
      <div className="flex min-w-0 flex-1 basis-48 flex-wrap items-center gap-x-2 gap-y-0.5">
        <span className="flex max-w-full min-w-0 items-center gap-2">
          <IntelligenceAvatar intelligence={who} size="sm" decorative />
          <Link
            id={state.labelId}
            to="/contacts/$id"
            params={{ id: who.id }}
            tabIndex={tab}
            className="min-w-0 truncate rounded-sm font-semibold tracking-[-0.01em] outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring/70"
          >
            {name}
          </Link>
        </span>
        <span
          id={state.descId}
          className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs text-muted-foreground"
        >
          <span className="max-w-full truncate font-mono">@{who.handle}</span>
          <YouBadge intelligence={who} viewerId={viewerId} />
          {m.permission === PermissionAdmin ? (
            <Badge variant="secondary" className="px-1.5 py-0 text-[0.68rem] leading-5">
              {t("profile.permissionAdmin")}
            </Badge>
          ) : null}
          {m.permission === PermissionGuest ? (
            <Badge variant="secondary" className="px-1.5 py-0 text-[0.68rem] leading-5">
              {t("profile.permissionGuest")}
            </Badge>
          ) : null}
          {isAI ? <StatusDot online={online} label={online ? t("presence.online") : t("presence.offline")} /> : null}
        </span>
      </div>
      {m.can_move || m.can_delete ? (
        <div className="flex flex-wrap items-center gap-1.5">
          {m.can_move ? (
            <Button
              variant="outline"
              size="sm"
              tabIndex={tab}
              aria-label={t("orgs.chartMove", { name })}
              onClick={() => onMove(m)}
            >
              <MoveRightIcon aria-hidden="true" />
              {t("orgs.move")}
            </Button>
          ) : null}
          {deletes ? (
            <Button
              variant="outline"
              size="sm"
              tabIndex={tab}
              className="text-destructive hover:text-destructive"
              aria-label={t("orgs.chartDelete", { name })}
              onClick={() => onDelete(m)}
            >
              <Trash2Icon aria-hidden="true" />
              {t("orgs.deleteMember")}
            </Button>
          ) : null}
          {removes ? (
            <Button
              variant="outline"
              size="sm"
              tabIndex={tab}
              className="text-destructive hover:text-destructive"
              aria-label={t("orgs.chartRemove", { name, org: orgName })}
              onClick={() => onRemove(m)}
            >
              <UserMinusIcon aria-hidden="true" />
              {t("orgs.removeShort")}
            </Button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
