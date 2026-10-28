import { ChevronRightIcon } from "lucide-react";
import { useId, useState } from "react";
import { ContactRow } from "@/components/contact-row";
import { platformLabel } from "@/components/message-item";
import { usePeople } from "@/entities/intelligence";
import type { PeopleGroup as PeopleGroupData } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";

/*
 * The people one connector has seen (CONT-01), closed until asked: a connector can have seen
 * hundreds, and each page of them is a request. With a filter on (`query`, already settled) the
 * connector opens by itself and the server searches its people, so what is shown is what matched.
 */
export function PeopleGroup({ group, query }: { group: PeopleGroupData; query: string }) {
  const { t } = useI18n();
  const listId = useId();
  const { connector, count } = group;
  const searching = query !== "";
  // null: not chosen yet, so a search opens it and no search keeps it closed.
  const [chosen, setChosen] = useState<boolean | null>(null);
  const expanded = count > 0 && (chosen ?? searching);
  const people = usePeople(connector.id, query, expanded);
  const items = people.data?.pages.flatMap((page) => page.items) ?? [];
  const settled = !people.isPending && !people.isFetching && !people.isPlaceholderData;

  const title = (
    <>
      <span className="min-w-0 truncate font-semibold">{connector.name}</span>
      <span className="shrink-0 text-xs text-muted-foreground">
        {platformLabel(connector.kind)}
      </span>
      <span className="ml-auto shrink-0 font-mono text-xs text-muted-foreground tabular">
        {count === 1 ? t("contacts.peopleOne") : t("contacts.peopleMany", { count })}
      </span>
    </>
  );

  return (
    <div className="border-b">
      {count > 0 ? (
        <button
          type="button"
          aria-expanded={expanded}
          aria-controls={listId}
          onClick={() => setChosen(!expanded)}
          className="flex w-full items-center gap-3 px-4 py-3 text-left outline-none hover:bg-accent/40 focus-visible:bg-accent/40 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset md:px-8"
        >
          <ChevronRightIcon
            className={cn(
              "size-4 shrink-0 text-muted-foreground transition-transform",
              expanded && "rotate-90",
            )}
            aria-hidden="true"
          />
          {title}
        </button>
      ) : (
        <div className="flex items-center gap-3 px-4 py-3 md:px-8">
          <span className="size-4 shrink-0" aria-hidden="true" />
          {title}
        </div>
      )}
      {expanded ? (
        <div id={listId}>
          {people.isError && !people.data ? (
            <div role="alert" className="border-t px-4 py-4 text-sm md:px-8">
              <p className="text-destructive">
                {t("contacts.peopleLoadError", { name: connector.name })}
              </p>
              <Button
                variant="outline"
                size="sm"
                className="mt-3"
                onClick={() => void people.refetch()}
              >
                {t("common.retry")}
              </Button>
            </div>
          ) : people.isPending ? (
            <div className="space-y-3 border-t px-4 py-4 md:px-8" aria-hidden="true">
              <Skeleton className="h-4 w-40" />
              <Skeleton className="h-4 w-56 max-w-full" />
            </div>
          ) : (
            <>
              {items.length > 0 ? (
                <ul aria-label={connector.name} className="divide-y border-t">
                  {items.map((person) => (
                    <li key={person.id}>
                      {/* People are read-only history: answering happens on their platform. */}
                      <ContactRow
                        testId={`contact-${person.id}`}
                        className="px-4 md:px-8"
                        intelligence={person}
                      />
                    </li>
                  ))}
                </ul>
              ) : settled ? (
                <p
                  role="status"
                  className="border-t px-4 py-3 text-sm text-muted-foreground md:px-8"
                >
                  {t("contacts.peopleNoMatch")}
                </p>
              ) : null}
              {people.hasNextPage ? (
                <div className="border-t px-4 py-3 md:px-8">
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={people.isFetchingNextPage}
                    onClick={() => void people.fetchNextPage()}
                  >
                    {people.isFetchingNextPage ? t("common.loading") : t("contacts.showMore")}
                  </Button>
                </div>
              ) : null}
            </>
          )}
        </div>
      ) : null}
    </div>
  );
}
