import type { QueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/shared/api/query-keys";
import type { RoomMember } from "@/shared/api/types.gen";

/*
 * What the live frames do to the cached member lists of the rooms.
 */

/** A room's members changed (room.members): refetch them and the sidebar, whose search reads names. */
export function membersChanged(qc: QueryClient, roomId: string) {
  qc.invalidateQueries({ queryKey: queryKeys.roomMembers(roomId) });
  qc.invalidateQueries({ queryKey: queryKeys.conversations, exact: true });
}

/** An AI came online or went offline: its member rows in every cached room show it at once. */
export function setAgentOnline(qc: QueryClient, agentId: string, online: boolean) {
  for (const [key, members] of qc.getQueriesData<RoomMember[]>({ queryKey: ["rooms"] })) {
    if (key[2] !== "members" || !members) continue;
    if (!members.some((mb) => mb.kind === "agent" && mb.member_id === agentId)) continue;
    qc.setQueryData<RoomMember[]>(
      key,
      members.map((mb) =>
        mb.kind === "agent" && mb.member_id === agentId ? { ...mb, online } : mb,
      ),
    );
    qc.invalidateQueries({ queryKey: key, exact: true });
  }
}
