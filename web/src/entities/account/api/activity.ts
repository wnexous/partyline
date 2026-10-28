import { queryOptions, useQuery } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import type { ActivityItem, List } from "@/shared/api/types.gen";
import { ACTIVITY_FETCH, trimActivity } from "../lib/activity";

/**
 * The dashboard feed: latest messages across the rooms the user can see, newest first, from
 * one `GET /activity` request. Live room.message events keep it current (app/live/dispatch.ts).
 */
export const activityQuery = () =>
  queryOptions({
    queryKey: queryKeys.activity,
    queryFn: ({ signal }) =>
      api<List<ActivityItem>>("GET", "/activity", undefined, {
        query: { limit: ACTIVITY_FETCH },
        signal,
      }).then((list) => trimActivity(list.items)),
  });

export const useActivity = () => useQuery(activityQuery());
