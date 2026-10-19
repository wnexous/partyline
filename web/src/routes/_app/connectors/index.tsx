import { createFileRoute } from "@tanstack/react-router";
import { PlugZapIcon } from "lucide-react";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { useT } from "@/lib/i18n";

export const Route = createFileRoute("/_app/connectors/")({
  component: ConnectorsPage,
});

function ConnectorsPage() {
  const t = useT();
  return (
    <>
      <PageHeader title={t("page.connectorsTitle")} description={t("page.connectorsBody")} />
      <EmptyState icon={PlugZapIcon} title={t("page.emptyTitle")} />
    </>
  );
}
