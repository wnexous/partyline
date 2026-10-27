import { createFileRoute, Link } from "@tanstack/react-router";
import { ChevronRightIcon, PlugZapIcon, PlusIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { AddConnectorDialog } from "@/components/add-connector-dialog";
import { ConnectorStatusBadge, useConnectorState } from "@/components/connector-status";
import { EmptyState } from "@/components/empty-state";
import { platformLabel } from "@/components/message-item";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useConnectors, useOrg } from "@/lib/api/queries";
import type { Connector } from "@/lib/api/types.gen";
import { useT } from "@/lib/i18n";
import { isAdminOf } from "@/lib/orgs";

export const Route = createFileRoute("/_app/orgs/$orgId/connections/")({
  component: OrgConnectionsPage,
});

/*
 * The connections of the organization in the address (SET-05, ORG-02): the URL and the data agree
 * because the list filters on `org_id` and its address names the organization. Its members see
 * them and its admins add and change them (ORG-03, OMEM-04); the server decides who may.
 */
function OrgConnectionsPage() {
  const t = useT();
  const { orgId } = Route.useParams();
  const org = useOrg(orgId).data;
  // The pages work on the id: an address that is the organization's slug is replaced by it.
  const id = org?.id ?? orgId;
  const connectors = useConnectors();
  const [adding, setAdding] = useState(false);
  const sorted = useMemo(
    () =>
      (connectors.data ?? [])
        .filter((c) => c.org_id === id)
        .sort((a, b) => a.name.localeCompare(b.name)),
    [connectors.data, id],
  );
  // Only an admin of this organization adds a connection to it.
  const canAdd = org ? isAdminOf(org) : false;

  const add = canAdd ? (
    <Button onClick={() => setAdding(true)}>
      <PlusIcon aria-hidden="true" />
      {t("connectors.add")}
    </Button>
  ) : null;

  return (
    <>
      <PageHeader
        title={t("page.connectorsTitle")}
        description={t("connectors.bodyOrg")}
        actions={sorted.length > 0 ? add : null}
      />
      {connectors.isPending ? (
        <ul aria-hidden="true" className="divide-y border-b">
          {[0, 1].map((i) => (
            <li key={i} className="space-y-2 px-4 py-4 md:px-8">
              <Skeleton className="h-4 w-40" />
              <Skeleton className="h-3 w-56 max-w-full" />
            </li>
          ))}
        </ul>
      ) : connectors.isError ? (
        <div className="px-4 py-10 text-sm md:px-8" role="alert">
          <p className="text-destructive">{t("connectors.loadError")}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => connectors.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : sorted.length === 0 ? (
        <EmptyState icon={PlugZapIcon} title={t("connectors.emptyTitle")} action={add}>
          <p>
            {canAdd
              ? t("connectors.emptyBody")
              : t("connectors.emptyReadOnly", { org: org?.name ?? "" })}
          </p>
        </EmptyState>
      ) : (
        <ul
          aria-label={t("connectors.listLabelOrg", { org: org?.name ?? "" })}
          className="divide-y border-b"
        >
          {sorted.map((c) => (
            <ConnectorRow key={c.id} orgId={id} connector={c} />
          ))}
        </ul>
      )}
      <AddConnectorDialog open={adding} onOpenChange={setAdding} />
    </>
  );
}

function ConnectorRow({ orgId, connector: c }: { orgId: string; connector: Connector }) {
  const { status, lastError } = useConnectorState(c);
  return (
    <li aria-label={c.name}>
      <Link
        to="/orgs/$orgId/connections/$connectorId"
        params={{ orgId, connectorId: c.id }}
        className="group grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 px-4 py-3.5 outline-none transition-colors hover:bg-accent/60 focus-visible:bg-accent focus-visible:ring-2 focus-visible:ring-ring/70 focus-visible:ring-inset md:px-8"
      >
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate font-semibold tracking-[-0.01em]">{c.name}</span>
          <span className="shrink-0 rounded-[3px] border px-1.5 py-px text-[0.7rem] text-muted-foreground">
            {platformLabel(c.kind)}
          </span>
        </span>
        <ChevronRightIcon
          className="row-span-2 size-4 text-muted-foreground transition-transform group-hover:translate-x-0.5"
          aria-hidden="true"
        />
        <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
          <ConnectorStatusBadge status={status} enabled={c.enabled} />
          {lastError ? (
            <span className="min-w-0 truncate font-mono text-xs text-destructive">{lastError}</span>
          ) : null}
        </span>
      </Link>
    </li>
  );
}
