import { getRouteApi, Link, useNavigate } from "@tanstack/react-router";
import { PencilRulerIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { useMe } from "@/entities/account";
import { useOrg, useOrgChart } from "@/entities/organization";
import {
  DeleteAIDialog,
  MoveMemberDialog,
  type MoveTarget,
  RemoveFromOrgDialog,
} from "@/features/manage-org-member";
import { isApiError } from "@/shared/api/client";
import type { IntelligenceRef } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { NotFound } from "@/shared/ui/molecules/not-found";
import { PageHeader } from "@/shared/ui/molecules/page-header";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { buildChartTree, ChartRow, ChartTree } from "@/widgets/org-chart";

const routeApi = getRouteApi("/_app/orgs/$orgId/chart");

/*
 * Who is where (CHART-01 to CHART-04): the positions as a tree, each with the identities that hold
 * it; a guest under whoever invited it, an AI that holds no position under its creator, and the
 * rest under "Unassigned". Every member of the organization, guests included, sees it (CHART-02).
 * What the viewer may do about each one is what the server's flags say (CHART-03): move between
 * positions, delete an AI of this organization, remove anyone else from it; admins can also go to
 * the positions editor. The tree is keyboard and screen-reader accessible (components/chart-tree.tsx).
 */
export function OrgChartPage() {
  const t = useT();
  const { orgId } = routeApi.useParams();
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
    return (
      <NotFound
        title={t("orgs.notFoundTitle")}
        body={t("orgs.notFoundBody")}
        action={
          <Button asChild variant="outline">
            <Link to="/orgs">{t("orgs.notFoundBack")}</Link>
          </Button>
        }
      />
    );
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
