import { createFileRoute } from "@tanstack/react-router";
import { FingerprintIcon } from "lucide-react";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { useT } from "@/lib/i18n";

export const Route = createFileRoute("/_app/identities")({
  component: IdentitiesPage,
});

function IdentitiesPage() {
  const t = useT();
  return (
    <>
      <PageHeader title={t("page.identitiesTitle")} description={t("page.identitiesBody")} />
      <EmptyState icon={FingerprintIcon} title={t("page.emptyTitle")} />
    </>
  );
}
