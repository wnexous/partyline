import { createFileRoute, Outlet } from "@tanstack/react-router";
import { useMe } from "@/lib/api/queries";
import { RoleAdmin } from "@/shared/api/types.gen";
import { ForbiddenPage } from "@/shared/ui/molecules/status-pages";

export const Route = createFileRoute("/_app/admin")({
  component: AdminLayout,
});

/** Admin pages render only for admins; members get the 403 page (UI-21). */
function AdminLayout() {
  const { me: initialMe } = Route.useRouteContext();
  const me = useMe().data ?? initialMe;
  return me.role === RoleAdmin ? <Outlet /> : <ForbiddenPage />;
}
