import {
  infiniteQueryOptions,
  keepPreviousData,
  queryOptions,
  useInfiniteQuery,
  useQuery,
} from "@tanstack/react-query";
import { api } from "@/shared/api/client";
import { queryKeys } from "@/shared/api/query-keys";
import {
  type Contacts,
  DefaultPeoplePage,
  type IntelligenceRef,
  type Page,
} from "@/shared/api/types.gen";

/** Everyone the caller can reach, grouped as the contacts page shows them (GET /contacts). */
export const contactsQuery = () =>
  queryOptions({
    queryKey: queryKeys.contacts,
    queryFn: ({ signal }) => api<Contacts>("GET", "/contacts", undefined, { signal }),
  });

export const useContacts = () => useQuery(contactsQuery());

/**
 * The people of one connector in name order, a page at a time; the cursor is the id of the last
 * person of the page before. `q` searches names, handles and account ids on the server, and the
 * previous search stays on screen while the next one loads.
 */
export const peopleQuery = (connectorId: string, q = "") =>
  infiniteQueryOptions({
    queryKey: queryKeys.people(connectorId, q),
    queryFn: ({ pageParam, signal }) =>
      api<Page<IntelligenceRef>>("GET", "/contacts/people", undefined, {
        query: {
          connector_id: connectorId,
          q: q || undefined,
          after: pageParam,
          limit: DefaultPeoplePage,
        },
        signal,
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page: Page<IntelligenceRef>) =>
      page.has_more ? page.items.at(-1)?.id : undefined,
  });

export const usePeople = (connectorId: string, q = "", enabled = true) =>
  useInfiniteQuery({ ...peopleQuery(connectorId, q), enabled, placeholderData: keepPreviousData });
