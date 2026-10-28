import { createFileRoute } from "@tanstack/react-router";
import { UsersIcon } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";
import { EmptyState } from "@/shared/ui/molecules/empty-state";
import { PageHeader } from "@/shared/ui/molecules/page-header";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/shared/ui/primitives/alert-dialog";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { Switch } from "@/shared/ui/primitives/switch";
import { ToggleGroup, ToggleGroupItem } from "@/shared/ui/primitives/toggle-group";
import { useAdminUsers, useMe, useUpdateUser } from "@/lib/api/queries";
import { type AdminUser, type Role, RoleAdmin, RoleMember } from "@/shared/api/types.gen";
import { useI18n, useT } from "@/shared/i18n";
import { formatAbsolute } from "@/shared/lib/time";
import { cn } from "@/shared/lib/utils";

export const Route = createFileRoute("/_app/admin/users")({
  component: AdminUsersPage,
});

/*
 * Accounts (UI-20, ACL-08): admins change roles and disable or enable accounts. Disabling ends
 * the user's sessions on the server, so it asks first; admins cannot demote or disable
 * themselves from here (that would lock the instance out of its own admin page).
 */

const displayName = (u: Pick<AdminUser, "display_name" | "username">) =>
  u.display_name || u.username;

function AdminUsersPage() {
  const t = useT();
  const users = useAdminUsers();
  const [disabling, setDisabling] = useState<AdminUser | null>(null);
  const list = users.data ?? [];

  return (
    <>
      <PageHeader title={t("page.adminUsersTitle")} description={t("page.adminUsersBody")} />
      {users.isPending ? (
        <div className="space-y-3 px-4 py-6 md:px-8" aria-hidden="true">
          <Skeleton className="h-4 w-48" />
          <Skeleton className="h-4 w-64 max-w-full" />
        </div>
      ) : users.isError ? (
        <div className="px-4 py-10 text-sm md:px-8" role="alert">
          <p className="text-destructive">{t("admin.usersError")}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => users.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : list.length === 0 ? (
        <EmptyState icon={UsersIcon} title={t("page.emptyTitle")} />
      ) : (
        <ul aria-label={t("admin.usersList")} className="divide-y border-b">
          {list.map((u) => (
            <UserRow key={u.id} user={u} onDisable={() => setDisabling(u)} />
          ))}
        </ul>
      )}
      <DisableDialog user={disabling} onClose={() => setDisabling(null)} />
    </>
  );
}

function UserRow({ user: u, onDisable }: { user: AdminUser; onDisable: () => void }) {
  const { t, locale } = useI18n();
  const me = useMe().data;
  const update = useUpdateUser();
  const self = me?.id === u.id;
  const switchId = useId();
  const name = displayName(u);

  const setRole = (role: string) => {
    if (!role || role === u.role) return;
    update.mutate(
      { id: u.id, role: role as Role },
      { onError: () => toast.error(t("admin.updateFailed", { name })) },
    );
  };
  const setActive = (active: boolean) => {
    if (!active) return onDisable();
    update.mutate(
      { id: u.id, disabled: false },
      {
        onSuccess: () => toast(t("admin.enabled", { name })),
        onError: () => toast.error(t("admin.updateFailed", { name })),
      },
    );
  };

  return (
    <li
      aria-label={name}
      className="flex flex-col gap-3 px-4 py-3.5 sm:flex-row sm:items-center md:px-8"
    >
      <div className={cn("min-w-0 flex-1", u.disabled && "text-muted-foreground")}>
        <p className="flex min-w-0 flex-wrap items-center gap-2">
          <span className="truncate font-semibold tracking-[-0.01em]">{name}</span>
          {self ? (
            <span className="rounded-[3px] bg-tape px-1.5 py-px text-[0.7rem] font-semibold text-tape-foreground">
              {t("admin.you")}
            </span>
          ) : null}
          {u.disabled ? (
            <span className="rounded-[3px] bg-muted px-1.5 py-px text-[0.7rem] font-medium text-muted-foreground">
              {t("admin.disabled")}
            </span>
          ) : null}
        </p>
        <p className="flex flex-wrap gap-x-3 text-xs text-muted-foreground">
          <span className="font-mono">@{u.username}</span>
          <span>{t("admin.joined", { date: formatAbsolute(u.created_at, locale) })}</span>
        </p>
      </div>
      <div className="flex items-center gap-4">
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          value={u.role}
          onValueChange={setRole}
          disabled={self || update.isPending}
          aria-label={t("admin.roleFor", { name })}
        >
          <ToggleGroupItem value={RoleMember} className="px-3">
            {t("admin.role.member")}
          </ToggleGroupItem>
          <ToggleGroupItem value={RoleAdmin} className="px-3">
            {t("admin.role.admin")}
          </ToggleGroupItem>
        </ToggleGroup>
        <div className="flex items-center gap-2">
          <Switch
            id={switchId}
            checked={!u.disabled}
            onCheckedChange={setActive}
            disabled={self || update.isPending}
          />
          <label htmlFor={switchId} className="text-sm">
            {t("admin.active")}
          </label>
        </div>
      </div>
    </li>
  );
}

function DisableDialog({ user, onClose }: { user: AdminUser | null; onClose: () => void }) {
  const t = useT();
  const update = useUpdateUser();
  const name = user ? displayName(user) : "";
  return (
    <AlertDialog open={user !== null} onOpenChange={(open) => (open ? null : onClose())}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("admin.disableTitle", { name })}</AlertDialogTitle>
          <AlertDialogDescription>{t("admin.disableBody")}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            disabled={update.isPending}
            onClick={(e) => {
              e.preventDefault();
              if (!user) return;
              update.mutate(
                { id: user.id, disabled: true },
                {
                  onSuccess: () => {
                    toast(t("admin.disabledToast", { name }));
                    onClose();
                  },
                  onError: () => toast.error(t("admin.updateFailed", { name })),
                },
              );
            }}
          >
            {t("admin.disableConfirm")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
