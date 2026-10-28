import { createFileRoute } from "@tanstack/react-router";
import { IdentitiesPage } from "@/pages/identities";

export const Route = createFileRoute("/_app/identities")({
  component: IdentitiesPage,
});
