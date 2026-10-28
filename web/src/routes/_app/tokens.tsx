import { createFileRoute } from "@tanstack/react-router";
import { TokensPage } from "@/pages/tokens";

export const Route = createFileRoute("/_app/tokens")({
  component: TokensPage,
});
