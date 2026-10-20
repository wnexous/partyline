import { createFileRoute } from "@tanstack/react-router";
import { BotIcon } from "lucide-react";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { useT } from "@/lib/i18n";

export const Route = createFileRoute("/_app/agents")({
  component: AgentsPage,
});

function AgentsPage() {
  const t = useT();
  return (
    <>
      <PageHeader title={t("page.agentsTitle")} description={t("page.agentsBody")} />
      <EmptyState icon={BotIcon} title={t("page.emptyTitle")} />
    </>
  );
}
