import { createFileRoute } from "@tanstack/react-router";
import { MessagesSquareIcon } from "lucide-react";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { useT } from "@/lib/i18n";

export const Route = createFileRoute("/_app/rooms/")({
  component: RoomsPage,
});

function RoomsPage() {
  const t = useT();
  return (
    <>
      <PageHeader title={t("page.roomsTitle")} description={t("page.roomsBody")} />
      <EmptyState icon={MessagesSquareIcon} title={t("page.emptyTitle")} />
    </>
  );
}
