import {
  type QueryClient,
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import type { List, NotifyRequest, RoomNotification } from "@/shared/api/types.gen";
import type { NotificationSetting } from "../model/notifications";

/** The path segment the owner's route uses for the principal, whose label is "" (SN-08). */
const principalLabel = "_principal";

/** The owner's notifications route of one session of an AI (SN-08). */
export function sessionNotificationsPath(agentId: string, label: string): string {
  return `/agents/${agentId}/sessions/${encodeURIComponent(label || principalLabel)}/notifications`;
}

/** The notification state of one session in every room of its AI, oldest room first as the server lists it. */
export const sessionNotificationsQuery = (agentId: string, label: string, enabled = true) =>
  queryOptions({
    queryKey: queryKeys.agentSessionNotifications(agentId, label),
    queryFn: () =>
      api<List<RoomNotification>>("GET", sessionNotificationsPath(agentId, label)).then(items),
    enabled: enabled && agentId !== "",
  });

export const useSessionNotifications = (agentId: string, label: string, enabled = true) =>
  useQuery(sessionNotificationsQuery(agentId, label, enabled));

/** What a change of one room's setting answers and leaves in the cache (SN-08). */
function storeNotification(
  qc: QueryClient,
  agentId: string,
  label: string,
  updated: RoomNotification,
) {
  qc.setQueryData<RoomNotification[]>(queryKeys.agentSessionNotifications(agentId, label), (old) =>
    old?.map((room) => (room.room_id === updated.room_id ? updated : room)),
  );
  // The member card carries the same setting per session, and any open room is one of them.
  qc.invalidateQueries({ queryKey: queryKeys.rooms });
}

/**
 * Changes the notification setting of one session in one room (SN-08). Who may: whoever manages the AI;
 * the server answers 403 for everyone else, so the controls are only offered with `my_level: "manage"`.
 */
export function useSetSessionNotification(agentId: string, label: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ roomId, notify }: { roomId: string; notify: NotificationSetting }) =>
      api<RoomNotification>(
        "PUT",
        `${sessionNotificationsPath(agentId, label)}/${encodeURIComponent(roomId)}`,
        { notify } satisfies NotifyRequest,
      ),
    onSuccess: (updated) => storeNotification(qc, agentId, label, updated),
  });
}
