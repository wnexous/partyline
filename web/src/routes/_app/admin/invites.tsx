import { createFileRoute } from "@tanstack/react-router";
import { AdminInvitesPage } from "@/pages/admin";

export const Route = createFileRoute("/_app/admin/invites")({
  component: AdminInvitesPage,
});
