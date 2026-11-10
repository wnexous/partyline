import { createFileRoute } from "@tanstack/react-router";
import { AdminSettingsPage } from "@/pages/admin";

export const Route = createFileRoute("/_app/admin/settings")({
  component: AdminSettingsPage,
});
