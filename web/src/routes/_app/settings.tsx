import { createFileRoute } from "@tanstack/react-router";
import { SettingsIcon } from "lucide-react";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { useT } from "@/lib/i18n";

export const Route = createFileRoute("/_app/settings")({
  component: SettingsPage,
});

function SettingsPage() {
  const t = useT();
  return (
    <>
      <PageHeader title={t("page.settingsTitle")} description={t("page.settingsBody")} />
      <EmptyState icon={SettingsIcon} title={t("page.emptyTitle")} />
    </>
  );
}
