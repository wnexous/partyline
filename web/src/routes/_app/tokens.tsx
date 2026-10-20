import { createFileRoute } from "@tanstack/react-router";
import { KeyRoundIcon } from "lucide-react";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { useT } from "@/lib/i18n";

export const Route = createFileRoute("/_app/tokens")({
  component: TokensPage,
});

function TokensPage() {
  const t = useT();
  return (
    <>
      <PageHeader title={t("page.tokensTitle")} description={t("page.tokensBody")} />
      <EmptyState icon={KeyRoundIcon} title={t("page.emptyTitle")} />
    </>
  );
}
