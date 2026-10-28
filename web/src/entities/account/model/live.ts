import type { QueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/shared/api/query-keys";
import type { ActivityItem, Identity, Message, Room, RoomRef } from "@/shared/api/types.gen";
import { trimActivity } from "../lib/activity";
import type { LinkedIdentityState } from "./linked-identity-store";

/*
 * What the live frames do to the account's own data: the dashboard feed and the identities.
 */

/** The room's name as the cache knows it: the feed itself, the room, or the rooms list. */
function roomRef(qc: QueryClient, feed: ActivityItem[], roomId: string): RoomRef | undefined {
  const known = feed.find((a) => a.room.id === roomId)?.room;
  if (known) return known;
  const room =
    qc.getQueryData<Room>(queryKeys.room(roomId)) ??
    qc.getQueryData<Room[]>(queryKeys.rooms)?.find((r) => r.id === roomId);
  return room ? { id: room.id, name: room.name } : undefined;
}

/** Puts a live message on the dashboard feed, when the feed is cached. */
export function prependActivity(qc: QueryClient, m: Message) {
  const feed = qc.getQueryData<ActivityItem[]>(queryKeys.activity);
  if (!feed || feed.some((a) => a.message.id === m.id)) return;
  const room = roomRef(qc, feed, m.room_id);
  if (!room) {
    // A room the cache cannot name (new or newly shared): the server's feed has it.
    qc.invalidateQueries({ queryKey: queryKeys.activity });
    return;
  }
  qc.setQueryData<ActivityItem[]>(
    queryKeys.activity,
    trimActivity([{ room, message: m }, ...feed]),
  );
}

/** An identity was linked from a terminal (identity.linked): the pairing dialog hears of it. */
export function identityLinked(
  qc: QueryClient,
  setLastLinkedIdentity: LinkedIdentityState["setLastLinkedIdentity"],
  identity: Identity,
) {
  setLastLinkedIdentity(identity);
  qc.invalidateQueries({ queryKey: queryKeys.identities });
}
