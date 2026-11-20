import { queryOptions, useQuery } from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import type { Contacts } from "@/shared/api/types.gen";

/** Everyone the caller can reach, grouped as the contacts page shows them (GET /contacts). */
export const contactsQuery = () =>
  queryOptions({
    queryKey: queryKeys.contacts,
    queryFn: ({ signal }) => api<Contacts>("GET", "/contacts", undefined, { signal }),
  });

export const useContacts = () => useQuery(contactsQuery());
