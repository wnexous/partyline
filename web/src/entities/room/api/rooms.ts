import {
  type QueryClient,
  queryOptions,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { putAvatarImage } from "@/shared/api/avatar-upload";
import { api } from "@/shared/api/client";
import { items } from "@/shared/api/list";
import { queryKeys } from "@/shared/api/query-keys";
import type {
  ActivityItem,
  Conversation,
  CreateGroupRequest,
  CreateRoomRequest,
  List,
  OpenDMRequest,
  Room,
  UpdateRoomRequest,
} from "@/shared/api/types.gen";
import { patchConversation } from "../model/conversations-cache";

export const roomsQuery = () =>
  queryOptions({
    queryKey: queryKeys.rooms,
    queryFn: () => api<List<Room>>("GET", "/rooms").then(items),
  });

export const roomQuery = (id: string) =>
  queryOptions({
    queryKey: queryKeys.room(id),
    queryFn: () => api<Room>("GET", `/rooms/${id}`),
  });

export const useRooms = () => useQuery(roomsQuery());

export const useRoom = (id: string) => useQuery(roomQuery(id));

/** A room the user just made, opened or changed: cached, and refreshed in the rooms list and the sidebar. */
function storeRoom(qc: QueryClient, room: Room) {
  qc.setQueryData(queryKeys.room(room.id), room);
  qc.invalidateQueries({ queryKey: queryKeys.rooms, exact: true });
  qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });
}

export function useCreateRoom() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateRoomRequest) => api<Room>("POST", "/rooms", req),
    onSuccess: (room) => storeRoom(qc, room),
  });
}

export function useUpdateRoom(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: UpdateRoomRequest) => api<Room>("PATCH", `/rooms/${id}`, req),
    onSuccess: (room) => storeRoom(qc, room),
  });
}

export function useDeleteRoom() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>("DELETE", `/rooms/${id}`),
    onSuccess: (_, id) => {
      // Same cache effect as the live room.deleted event (ROOMDEL-02): gone from every list now.
      qc.removeQueries({ queryKey: queryKeys.room(id) });
      qc.setQueryData<Room[]>(queryKeys.rooms, (old) => old?.filter((r) => r.id !== id));
      qc.setQueryData<Conversation[]>(queryKeys.conversations, (old) =>
        old?.filter((c) => c.id !== id),
      );
      qc.setQueryData<ActivityItem[]>(queryKeys.activity, (old) =>
        old?.filter((a) => a.room.id !== id),
      );
    },
  });
}

/** Shows a group's photo, or its absence, on the cached room, rooms list and sidebar row (GAV-07). */
function showRoomPhoto(qc: QueryClient, roomId: string, avatarUrl: string | undefined) {
  const photo = <T extends { avatar_url?: string }>({ avatar_url: _old, ...rest }: T) =>
    (avatarUrl ? { ...rest, avatar_url: avatarUrl } : rest) as unknown as T;
  qc.setQueryData<Room>(queryKeys.room(roomId), (old) => old && photo(old));
  qc.setQueryData<Room[]>(queryKeys.rooms, (old) =>
    old?.map((r) => (r.id === roomId ? photo(r) : r)),
  );
  patchConversation(qc, roomId, photo);
}

/** Uploads a group's photo: the image file goes as the raw body (GAV-01); 422 `avatar_invalid`. */
export function useUploadRoomAvatar(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (image: Blob) =>
      putAvatarImage<Room>(`/rooms/${encodeURIComponent(id)}/avatar`, image),
    onSuccess: (room) => {
      storeRoom(qc, room);
      showRoomPhoto(qc, room.id, room.avatar_url);
    },
  });
}

/** Removes a group's photo: its initials show again everywhere. */
export function useRemoveRoomAvatar(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<void>("DELETE", `/rooms/${encodeURIComponent(id)}/avatar`),
    onSuccess: () => {
      showRoomPhoto(qc, id, undefined);
      qc.invalidateQueries({ queryKey: queryKeys.room(id), exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.rooms, exact: true });
      qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });
    },
  });
}

export function useResumeRoom(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<Room>("POST", `/rooms/${id}/resume`),
    onSuccess: (room) => qc.setQueryData(queryKeys.room(id), room),
  });
}

/**
 * Takes the caller's own member row out of a group (ROOM-01): that is leaving, so the room goes
 * from every list at once, as the room.deleted event that follows it does.
 */
export function useLeaveRoom() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ roomId, memberId }: { roomId: string; memberId: string }) =>
      api<void>("DELETE", `/rooms/${roomId}/members/${memberId}`),
    onSuccess: (_, { roomId }) => {
      qc.removeQueries({ queryKey: queryKeys.room(roomId) });
      qc.setQueryData<Room[]>(queryKeys.rooms, (old) => old?.filter((r) => r.id !== roomId));
      qc.setQueryData<Conversation[]>(queryKeys.conversations, (old) =>
        old?.filter((c) => c.id !== roomId),
      );
      qc.setQueryData<ActivityItem[]>(queryKeys.activity, (old) =>
        old?.filter((a) => a.room.id !== roomId),
      );
    },
  });
}

/** Opens the DM with an intelligence (id): the existing one, or a new one (DM-01). */
export function useOpenDM() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (withId: string) =>
      api<Room>("POST", "/conversations/dm", { with: withId } satisfies OpenDMRequest),
    onSuccess: (room) => storeRoom(qc, room),
  });
}

/**
 * Creates a group with members (LAY-04). A 422 `members_refused` carries every refused member in
 * `ApiError.refused`; nothing is created then.
 */
export function useCreateGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: CreateGroupRequest) => api<Room>("POST", "/groups", req),
    onSuccess: (room) => storeRoom(qc, room),
  });
}
