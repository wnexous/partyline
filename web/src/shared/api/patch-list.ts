import type { QueryClient } from "@tanstack/react-query";

/** Replaces one item of a cached list, found by id; a list that is not cached stays absent. */
export function patchList<T extends { id: string }>(
  qc: QueryClient,
  key: readonly unknown[],
  id: string,
  patch: (item: T) => T,
) {
  qc.setQueryData<T[]>(key, (old) => old?.map((item) => (item.id === id ? patch(item) : item)));
}
