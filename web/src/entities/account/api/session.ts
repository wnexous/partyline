import {
  type QueryClient,
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import type { MeWithDefaultOrg } from "@/lib/orgs";
import { api } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import type {
  AcceptInviteRequest,
  ChangePasswordRequest,
  DeleteAccountRequest,
  InviteInfo,
  List,
  LoginRequest,
  Me,
  Session,
  SetupRequest,
  SetupStatus,
  UpdateMeRequest,
} from "@/shared/api/types.gen";

export const meQuery = () =>
  queryOptions({
    queryKey: queryKeys.me,
    queryFn: () => api<Me>("GET", "/me", undefined, { skipAuthRedirect: true }),
    staleTime: 60_000,
    retry: false,
  });

export const setupStatusQuery = () =>
  queryOptions({
    queryKey: queryKeys.setupStatus,
    queryFn: () => api<SetupStatus>("GET", "/setup/status"),
    staleTime: 0,
  });

export const inviteQuery = (token: string) =>
  queryOptions({
    queryKey: queryKeys.invite(token),
    queryFn: () => api<InviteInfo>("GET", `/invites/${encodeURIComponent(token)}`),
    retry: false,
  });

export const useMe = () => useQuery(meQuery());

export const useSetupStatus = () => useQuery(setupStatusQuery());

export const useInviteInfo = (token: string) => useQuery(inviteQuery(token));

/** Stores the logged-in user and drops every cached resource of a previous session. */
function startSession(qc: QueryClient, me: Me) {
  qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "me" });
  qc.setQueryData(queryKeys.me, me);
}

export function useSetup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: SetupRequest) => api<Me>("POST", "/setup", req, { skipAuthRedirect: true }),
    onSuccess: (me) => {
      qc.setQueryData<SetupStatus>(queryKeys.setupStatus, { needs_setup: false });
      startSession(qc, me);
    },
  });
}

export function useLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: LoginRequest) =>
      api<Me>("POST", "/auth/login", req, { skipAuthRedirect: true }),
    onSuccess: (me) => startSession(qc, me),
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<void>("POST", "/auth/logout", undefined, { skipAuthRedirect: true }),
    onSettled: () => qc.clear(),
  });
}

export function useAcceptInvite(token: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: AcceptInviteRequest) =>
      api<Me>("POST", `/invites/${encodeURIComponent(token)}/accept`, req, {
        skipAuthRedirect: true,
      }),
    onSuccess: (me) => startSession(qc, me),
  });
}

export function useUpdateMe() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: UpdateMeRequest) => api<Me>("PATCH", "/me", req),
    onSuccess: (me) => qc.setQueryData(queryKeys.me, me),
  });
}

export function useChangePassword() {
  return useMutation({
    mutationFn: (req: ChangePasswordRequest) => api<void>("POST", "/me/password", req),
  });
}

/**
 * Chooses the organization new AIs join when they are started without --org (ORG-04). The server
 * answers 403 for one the account does not belong to. `me` carries the default, so the cached
 * account is updated once the server has accepted it.
 */
export function useSetDefaultOrg() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (orgId: string) => api<unknown>("PUT", "/me/default-org", { org_id: orgId }),
    onSuccess: (_, orgId) =>
      qc.setQueryData<MeWithDefaultOrg>(
        queryKeys.me,
        (old) => old && { ...old, default_org_id: orgId },
      ),
  });
}

/**
 * Deletes the caller's own account (POS-06). The password proves who asks; a wrong one is a 422 on
 * its field, and the last active instance admin gets 409 `last_instance_admin`. The server ends the
 * session with the account, so nothing cached is kept.
 */
export function useDeleteAccount() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: DeleteAccountRequest) => api<void>("DELETE", "/me", req),
    onSuccess: () => qc.clear(),
  });
}

export const useSessions = () =>
  useQuery({
    queryKey: queryKeys.sessions,
    queryFn: () => api<List<Session>>("GET", "/me/sessions").then(items),
  });

export function useRevokeSession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/me/sessions/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.sessions }),
  });
}
