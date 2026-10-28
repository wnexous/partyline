import { createFileRoute } from "@tanstack/react-router";
import { AccountPage } from "@/pages/account";

export const Route = createFileRoute("/_app/account/")({
  component: AccountRoute,
});

function AccountRoute() {
  const { me } = Route.useRouteContext();
  return <AccountPage initialMe={me} />;
}
