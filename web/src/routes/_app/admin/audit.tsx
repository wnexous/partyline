import { createFileRoute } from "@tanstack/react-router";
import { AdminAuditPage } from "@/pages/admin";

export const Route = createFileRoute("/_app/admin/audit")({
  component: AdminAuditPage,
});
