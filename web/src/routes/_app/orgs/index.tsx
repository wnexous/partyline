import { createFileRoute } from "@tanstack/react-router";
import { OrgsPage } from "@/pages/orgs";

export const Route = createFileRoute("/_app/orgs/")({
  component: OrgsPage,
});
