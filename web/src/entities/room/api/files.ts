import { infiniteQueryOptions, useInfiniteQuery } from "@tanstack/react-query";
import type { FileFilter } from "@/shared/api/cache-types";
import { api } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import { DefaultPageSize, type Page, type RoomFile } from "@/shared/api/types.gen";

/** A room's files, newest first; fetchNextPage loads older ones (cursor = last id). */
export const roomFilesQuery = (roomId: string, kind: FileFilter) =>
  infiniteQueryOptions({
    queryKey: queryKeys.roomFiles(roomId, kind),
    queryFn: ({ pageParam, signal }) =>
      api<Page<RoomFile>>("GET", `/rooms/${roomId}/attachments`, undefined, {
        query: {
          kind: kind === "all" ? undefined : kind,
          before: pageParam,
          limit: DefaultPageSize,
        },
        signal,
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page: Page<RoomFile>) =>
      page.has_more ? page.items[page.items.length - 1]?.id : undefined,
  });

export const useRoomFiles = (roomId: string, kind: FileFilter) =>
  useInfiniteQuery(roomFilesQuery(roomId, kind));
