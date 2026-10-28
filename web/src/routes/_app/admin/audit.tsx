import { createFileRoute } from "@tanstack/react-router";
import { ScrollTextIcon } from "lucide-react";
import { EmptyState } from "@/shared/ui/molecules/empty-state";
import { PageHeader } from "@/shared/ui/molecules/page-header";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { useAudit } from "@/lib/api/queries";
import type { AuditEntry } from "@/shared/api/types.gen";
import { useI18n, useT } from "@/shared/i18n";
import { formatAbsolute, formatRelative } from "@/shared/lib/time";

export const Route = createFileRoute("/_app/admin/audit")({
  component: AdminAuditPage,
});

/** Security events, newest first, 50 per page (`before=<last id>`). */
function AdminAuditPage() {
  const t = useT();
  const audit = useAudit();
  const entries = audit.data?.pages.flatMap((p) => p.items) ?? [];

  return (
    <>
      <PageHeader title={t("page.adminAuditTitle")} description={t("page.adminAuditBody")} />
      {audit.isPending ? (
        <div className="space-y-3 px-4 py-6 md:px-8" aria-hidden="true">
          <Skeleton className="h-4 w-64 max-w-full" />
          <Skeleton className="h-4 w-56 max-w-full" />
        </div>
      ) : audit.isError ? (
        <div className="px-4 py-10 text-sm md:px-8" role="alert">
          <p className="text-destructive">{t("audit.loadError")}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => audit.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : entries.length === 0 ? (
        <EmptyState icon={ScrollTextIcon} title={t("audit.emptyTitle")}>
          <p>{t("audit.emptyBody")}</p>
        </EmptyState>
      ) : (
        <>
          <ul aria-label={t("nav.adminAudit")} className="divide-y border-b">
            {entries.map((e) => (
              <AuditRow key={e.id} entry={e} />
            ))}
          </ul>
          {audit.hasNextPage ? (
            <div className="px-4 py-4 md:px-8">
              <Button
                variant="outline"
                size="sm"
                disabled={audit.isFetchingNextPage}
                onClick={() => audit.fetchNextPage()}
              >
                {audit.isFetchingNextPage ? t("common.loading") : t("audit.more")}
              </Button>
            </div>
          ) : null}
        </>
      )}
    </>
  );
}

function detailText(detail: AuditEntry["detail"]): string {
  if (!detail) return "";
  return Object.entries(detail)
    .map(([k, v]) => `${k}=${typeof v === "string" ? v : JSON.stringify(v)}`)
    .join(" ");
}

function AuditRow({ entry: e }: { entry: AuditEntry }) {
  const { t, locale } = useI18n();
  const detail = detailText(e.detail);
  return (
    <li className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-0.5 px-4 py-2.5 md:grid-cols-[9rem_minmax(0,1fr)_auto] md:px-8">
      <span
        className="order-2 text-right font-mono text-xs text-muted-foreground tabular md:order-none md:text-left"
        title={formatAbsolute(e.created_at, locale)}
      >
        {formatRelative(e.created_at, locale)}
      </span>
      <div className="order-1 min-w-0 md:order-none">
        <p className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-sm">
          <code className="font-mono text-[0.8rem] font-semibold">{e.action}</code>
          <span className="text-muted-foreground">
            {e.actor ? `@${e.actor.username}` : t("audit.system")}
          </span>
          {e.target_kind ? (
            <span className="truncate font-mono text-xs text-muted-foreground">
              {e.target_kind}:{e.target_id}
            </span>
          ) : null}
        </p>
        {detail ? (
          <p className="truncate font-mono text-xs text-muted-foreground">{detail}</p>
        ) : null}
      </div>
      <span className="order-3 col-span-2 font-mono text-xs text-muted-foreground md:col-span-1">
        {e.ip}
      </span>
    </li>
  );
}
