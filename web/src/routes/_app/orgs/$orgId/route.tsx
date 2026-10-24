import { createFileRoute, Link, Outlet, useRouter } from "@tanstack/react-router";
import { ArrowLeftIcon, CompassIcon } from "lucide-react";
import { useEffect } from "react";
import { EmptyState } from "@/components/empty-state";
import { OrgAvatar } from "@/components/org-avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { isApiError } from "@/lib/api/client";
import type { Organization } from "@/lib/api/org-types";
import { useOrg } from "@/lib/api/queries";
import { useT } from "@/lib/i18n";
import { permissionLabel } from "@/lib/orgs";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/_app/orgs/$orgId")({
  component: OrgLayout,
});

/*
 * One organization's pages (CHART-02): the bar that names it and the sections of it, above the
 * page itself. Only its members see any of it: for anyone else the organization is not found,
 * whatever page of it they open. `{orgId}` may be the id or the address (slug); the pages work on
 * the id, so an address is replaced by it.
 */
function OrgLayout() {
  const t = useT();
  const { orgId } = Route.useParams();
  const router = useRouter();
  const org = useOrg(orgId);
  const id = org.data?.id;

  // The address opens the same page under the id, which the pages and the live refresh key on.
  useEffect(() => {
    if (!id || id === orgId) return;
    const { href } = router.state.location;
    const prefix = `/orgs/${orgId}`;
    if (!href.startsWith(prefix)) return;
    void router.navigate({ href: `/orgs/${id}${href.slice(prefix.length)}`, replace: true });
  }, [id, orgId, router]);

  if (org.isError && isApiError(org.error) && org.error.status === 404) {
    return (
      <EmptyState
        icon={CompassIcon}
        title={t("orgs.notFoundTitle")}
        action={
          <Button asChild variant="outline">
            <Link to="/orgs">{t("orgs.notFoundBack")}</Link>
          </Button>
        }
      >
        <p>{t("orgs.notFoundBody")}</p>
      </EmptyState>
    );
  }
  if (org.isError && !org.data) {
    return (
      <div className="px-4 py-10 text-sm md:px-8" role="alert">
        <p className="text-destructive">{t("orgs.orgLoadError")}</p>
        <Button variant="outline" size="sm" className="mt-3" onClick={() => org.refetch()}>
          {t("common.retry")}
        </Button>
      </div>
    );
  }
  if (!org.data) {
    return (
      <div aria-hidden="true" className="space-y-3 px-4 pt-6 md:px-8">
        <Skeleton className="h-4 w-28" />
        <div className="flex items-center gap-3">
          <Skeleton className="size-10" />
          <Skeleton className="h-6 w-48" />
        </div>
      </div>
    );
  }
  return (
    <>
      <OrgBar org={org.data} />
      <Outlet />
    </>
  );
}

const TAB =
  "-mb-px inline-flex h-11 shrink-0 items-center border-b-2 border-transparent px-3 text-sm font-medium text-muted-foreground outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:ring-inset data-[status=active]:border-primary data-[status=active]:text-foreground md:h-10";

/** The organization's name and the viewer's part in it, with the sections of it. */
function OrgBar({ org }: { org: Organization }) {
  const t = useT();
  return (
    <div data-testid="org-bar" className="border-b bg-background px-4 pt-4 md:px-8 md:pt-6">
      <Link
        to="/orgs"
        className="-ml-1 inline-flex items-center gap-1.5 rounded-md px-1 py-1 text-sm text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50"
      >
        <ArrowLeftIcon className="size-4" aria-hidden="true" />
        {t("orgs.backToList")}
      </Link>
      <div className="mt-3 flex min-w-0 items-center gap-3">
        <OrgAvatar
          org={{
            id: org.id,
            name: org.name,
            ...(org.avatar_url ? { avatarUrl: org.avatar_url } : {}),
          }}
          size="lg"
        />
        <div className="min-w-0">
          <p className="truncate text-lg font-semibold tracking-[-0.015em]">{org.name}</p>
          <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            <span className="truncate font-mono">{org.slug}</span>
            <Badge variant="secondary" className="px-1.5 py-0 text-[0.68rem] leading-5">
              {permissionLabel(t, org.permission)}
            </Badge>
          </p>
        </div>
      </div>
      <nav
        aria-label={t("orgs.sections", { org: org.name })}
        className={cn("-mx-1 mt-2 flex overflow-x-auto px-1")}
      >
        <Link to="/orgs/$orgId/settings" params={{ orgId: org.id }} className={TAB}>
          {t("orgs.tabSettings")}
        </Link>
      </nav>
    </div>
  );
}
