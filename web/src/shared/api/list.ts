import type { List } from "@/shared/api/types.gen";

export const items = <T>(list: List<T>) => list.items;
