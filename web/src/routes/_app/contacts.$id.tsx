import { createFileRoute } from "@tanstack/react-router";
import { ProfilePage } from "@/pages/contacts";

export const Route = createFileRoute("/_app/contacts/$id")({
  component: ProfilePage,
});
