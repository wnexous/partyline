import { getRouteApi, Outlet } from "@tanstack/react-router";
import { useMe } from "@/entities/account";
import { RoleAdmin } from "@/shared/api/types.gen";
import { ForbiddenPage } from "@/shared/ui/molecules/status-pages";

const routeApi = getRouteApi("/_app/admin");

/** Admin pages render only for admins; members get the 403 page (UI-21). */
export function AdminLayout() {
  const { me: initialMe } = routeApi.useRouteContext();
  const me = useMe().data ?? initialMe;
  return me.role === RoleAdmin ? <Outlet /> : <ForbiddenPage />;
}
