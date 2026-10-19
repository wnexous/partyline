import { createFileRoute } from "@tanstack/react-router";
import { RadioTowerIcon } from "lucide-react";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { useT } from "@/lib/i18n";

export const Route = createFileRoute("/_app/")({
  component: OverviewPage,
});

function OverviewPage() {
  const t = useT();
  return (
    <>
      <PageHeader title={t("page.overviewTitle")} description={t("page.overviewBody")} />
      <EmptyState icon={RadioTowerIcon} title={t("page.emptyTitle")} />
    </>
  );
}
