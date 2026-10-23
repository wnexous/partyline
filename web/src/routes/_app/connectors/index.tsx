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
import { useConnectors, useMe } from "@/lib/api/queries";
import type { Connector } from "@/lib/api/types.gen";
import { useT } from "@/lib/i18n";

export const Route = createFileRoute("/_app/connectors/")({
  component: ConnectorsPage,
});

function ConnectorsPage() {
  const t = useT();
  const connectors = useConnectors();
  const [adding, setAdding] = useState(false);
  const sorted = useMemo(
    () => [...(connectors.data ?? [])].sort((a, b) => a.name.localeCompare(b.name)),
    [connectors.data],
  );

  const add = (
    <Button onClick={() => setAdding(true)}>
      <PlusIcon aria-hidden="true" />
      {t("connectors.add")}
    </Button>
  );

  return (
    <>
      <PageHeader
        title={t("page.connectorsTitle")}
        description={t("page.connectorsBody")}
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
          <p>{t("connectors.emptyBody")}</p>
        </EmptyState>
      ) : (
        <ul aria-label={t("connectors.listLabel")} className="divide-y border-b">
          {sorted.map((c) => (
            <ConnectorRow key={c.id} connector={c} />
          ))}
        </ul>
      )}
      <AddConnectorDialog open={adding} onOpenChange={setAdding} />
    </>
  );
}

function ConnectorRow({ connector: c }: { connector: Connector }) {
  const t = useT();
  const me = useMe().data;
  const { status, lastError } = useConnectorState(c);
  const foreign = me && c.owner.id !== me.id;
  return (
    <li aria-label={c.name}>
      <Link
        to="/connectors/$connectorId"
        params={{ connectorId: c.id }}
        className="group grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 px-4 py-3.5 outline-none transition-colors hover:bg-accent/60 focus-visible:bg-accent focus-visible:ring-2 focus-visible:ring-ring/70 focus-visible:ring-inset md:px-8"
      >
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate font-semibold tracking-[-0.01em]">{c.name}</span>
          <span className="shrink-0 rounded-[3px] border px-1.5 py-px text-[0.7rem] text-muted-foreground">
            {platformLabel(c.kind)}
          </span>
          {foreign ? (
            <span className="shrink-0 text-xs text-muted-foreground">
              {t("rooms.ownedBy", { username: c.owner.username })}
            </span>
          ) : null}
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
