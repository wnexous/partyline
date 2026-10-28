import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import type {
  AddMemberRequest,
  List,
  RoomMember,
  UpdateMemberRequest,
} from "@/shared/api/types.gen";

/** The members of a room; `enabled` keeps a caller that has no room yet from asking for one. */
export const useRoomMembers = (id: string, enabled = true) =>
  useQuery({
    queryKey: queryKeys.roomMembers(id),
    queryFn: () => api<List<RoomMember>>("GET", `/rooms/${id}/members`).then(items),
    enabled: enabled && id !== "",
  });

export function useAddMember(roomId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: AddMemberRequest) => api<RoomMember>("POST", `/rooms/${roomId}/members`, req),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.roomMembers(roomId) }),
  });
}

export function useUpdateMember(roomId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ memberId, ...req }: UpdateMemberRequest & { memberId: string }) =>
      api<RoomMember>("PATCH", `/rooms/${roomId}/members/${memberId}`, req),
    onSuccess: (member) =>
      qc.setQueryData<RoomMember[]>(queryKeys.roomMembers(roomId), (old) =>
        old?.map((m) => (m.id === member.id ? member : m)),
      ),
  });
}

export function useRemoveMember(roomId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (memberId: string) => api<void>("DELETE", `/rooms/${roomId}/members/${memberId}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.roomMembers(roomId) }),
  });
}
