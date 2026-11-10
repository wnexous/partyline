import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import type {
  AdminUser,
  AuditEntry,
  CreatedInvite,
  CreateInviteRequest,
  InstanceSettings,
  Invite,
  List,
  Page,
  UpdateInstanceSettingsRequest,
  UpdateUserRequest,
  User,
} from "@/shared/api/types.gen";

export const useUsers = () =>
  useQuery({
    queryKey: queryKeys.users,
    queryFn: () => api<List<User>>("GET", "/users").then(items),
  });

export const useAdminUsers = () =>
  useQuery({
    queryKey: queryKeys.adminUsers,
    queryFn: () => api<List<AdminUser>>("GET", "/admin/users").then(items),
  });

export function useUpdateUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...req }: UpdateUserRequest & { id: string }) =>
      api<AdminUser>("PATCH", `/admin/users/${id}`, req),
    onSuccess: (user) =>
      qc.setQueryData<AdminUser[]>(queryKeys.adminUsers, (old) =>
        old?.map((u) => (u.id === user.id ? user : u)),
      ),
  });
}

export const useAdminInvites = () =>
  useQuery({
    queryKey: queryKeys.adminInvites,
    queryFn: () => api<List<Invite>>("GET", "/admin/invites").then(items),
  });

export function useCreateInvite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateInviteRequest) => api<CreatedInvite>("POST", "/admin/invites", req),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.adminInvites }),
  });
}

export function useRevokeInvite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/admin/invites/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.adminInvites }),
  });
}

export const useAudit = () =>
  useInfiniteQuery({
    queryKey: queryKeys.adminAudit,
    queryFn: ({ pageParam, signal }) =>
      api<Page<AuditEntry>>("GET", "/admin/audit", undefined, {
        query: { before: pageParam },
        signal,
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => (page.has_more ? page.items.at(-1)?.id : undefined),
  });

/** The instance settings an admin changes (RET-01): the attachment retention. */
export const useInstanceSettings = () =>
  useQuery({
    queryKey: queryKeys.adminSettings,
    queryFn: () => api<InstanceSettings>("GET", "/admin/settings"),
  });

export function useUpdateInstanceSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: UpdateInstanceSettingsRequest) =>
      api<InstanceSettings>("PATCH", "/admin/settings", req),
    onSuccess: (settings) => qc.setQueryData(queryKeys.adminSettings, settings),
  });
}
