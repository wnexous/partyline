import { createFileRoute } from "@tanstack/react-router";
import { UsersIcon } from "lucide-react";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { useT } from "@/lib/i18n";

export const Route = createFileRoute("/_app/admin/users")({
  component: AdminUsersPage,
});

function AdminUsersPage() {
  const t = useT();
  return (
    <>
      <PageHeader title={t("page.adminUsersTitle")} description={t("page.adminUsersBody")} />
      <EmptyState icon={UsersIcon} title={t("page.emptyTitle")} />
    </>
  );
}
