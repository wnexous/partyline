import { createFileRoute } from "@tanstack/react-router";
import {
  GitMergeIcon,
  PlugIcon,
  SearchIcon,
  Trash2Icon,
  UsersRoundIcon,
  XIcon,
} from "lucide-react";
import { type ReactNode, useId, useMemo, useState } from "react";
import { ConnectAgentDialog } from "@/components/connect-agent-dialog";
import { AIMeta } from "@/components/contact-meta";
import { ContactRow } from "@/components/contact-row";
import { DeleteAIDialog } from "@/components/delete-ai-dialog";
import { EmptyState } from "@/components/empty-state";
import { MachineGroup } from "@/components/machine-group";
import { MergeAIDialog } from "@/components/merge-ai-dialog";
import { MessageButton } from "@/components/message-button";
import { PageHeader } from "@/components/page-header";
import { PeopleGroup } from "@/components/people-group";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { useContacts } from "@/lib/api/queries";
import type { AIContact, Contacts, IntelligenceRef } from "@/lib/api/types.gen";
import { aiText, filterMachines, mergeTargets, ownAIs, sortMachines } from "@/lib/contacts";
import { matchesTerms } from "@/lib/conversations";
import { useI18n } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";
import { withContactPresence } from "@/lib/presence";

export const Route = createFileRoute("/_app/contacts/")({
  component: ContactsPage,
});

/*
 * Contacts (CONT-01, TREE-01..06): every intelligence the user can reach. Their own AIs as a
 * tree of machines and folders, the unlinked ones (no folder: only to delete), the AIs shared with
 * them, the people their connectors have seen and the other users. One filter narrows all of it.
 * Each contact can be messaged (DM-01).
 */

const isEmpty = (c: Contacts) =>
  c.machines.length + c.unlinked.length + c.shared.length + c.users.length + c.people.length === 0;

function ContactsPage() {
  const { t } = useI18n();
  const contacts = useContacts();
  const presence = useLiveStore((s) => s.presence);
  const [filter, setFilter] = useState("");
  const [connecting, setConnecting] = useState(false);
  const [deleting, setDeleting] = useState<IntelligenceRef | null>(null);
  const [merging, setMerging] = useState<IntelligenceRef | null>(null);
  const text = filter.trim();
  // The lists narrow as the user types; the server's people search waits for the text to settle.
  const settled = useDebouncedValue(text);

  const data = contacts.data;
  // Merge targets are the owner's AIs, whatever the filter shows (MRG-01).
  const own = useMemo(() => (data ? ownAIs(data) : []), [data]);
  const mergeCandidates = data ? mergeTargets(data, merging?.id ?? "") : [];
  const canMerge = own.length > 1;
  const live = useMemo(
    () => (ai: AIContact) => withContactPresence(ai, presence[ai.intelligence.id]),
    [presence],
  );
  const view = useMemo(() => {
    if (!data) return null;
    const own = (ai: AIContact) => matchesTerms(aiText(ai, ai.machine), text);
    return {
      machines: sortMachines(filterMachines(data.machines, text), (ai) => live(ai).online),
      unlinked: data.unlinked.filter(own),
      shared: data.shared.filter(own),
      users: data.users.filter((u) => matchesTerms(`${u.display_name}\n@${u.handle}`, text)),
      // A search can only find people at connectors that have seen someone.
      people: text ? data.people.filter((g) => g.count > 0) : data.people,
    };
  }, [data, text, live]);

  const connect = (
    <Button onClick={() => setConnecting(true)}>
      <PlugIcon aria-hidden="true" />
      {t("connect.open")}
    </Button>
  );
  const showing = data !== undefined && !isEmpty(data);

  return (
    <>
      <PageHeader
        title={t("page.contactsTitle")}
        description={t("page.contactsBody")}
        actions={showing ? connect : null}
      />
      {contacts.isPending ? (
        <ListSkeleton />
      ) : !data || !view ? (
        <div className="px-4 py-10 text-sm md:px-8" role="alert">
          <p className="text-destructive">{t("contacts.loadError")}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => contacts.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : isEmpty(data) ? (
        <EmptyState icon={UsersRoundIcon} title={t("contacts.emptyTitle")} action={connect}>
          <p>{t("contacts.emptyBody")}</p>
        </EmptyState>
      ) : (
        <>
          <FilterBox value={filter} onChange={setFilter} />
          <div className="pb-10">
            {text === "" || view.machines.length > 0 ? (
              <Section title={t("contacts.myAIs")}>
                {view.machines.length > 0 ? (
                  view.machines.map((machine) => (
                    <MachineGroup key={machine.hostname} machine={machine} live={live} />
                  ))
                ) : (
                  // The page header has the one button that connects an AI.
                  <div className="space-y-1 px-4 pt-1 pb-2 text-sm text-muted-foreground md:px-8">
                    <p>{t("contacts.noAIs")}</p>
                    <p>{t("contacts.noAIsBody")}</p>
                  </div>
                )}
              </Section>
            ) : null}

            {view.unlinked.length > 0 ? (
              <Section title={t("contacts.unlinked")} description={t("contacts.unlinkedBody")}>
                <ul className="divide-y border-y">
                  {view.unlinked.map((ai) => {
                    const item = live(ai);
                    return (
                      <li key={item.intelligence.id}>
                        <ContactRow
                          testId={`contact-${item.intelligence.id}`}
                          className="px-4 md:px-8"
                          intelligence={item.intelligence}
                          meta={<AIMeta ai={item} lastSeen />}
                          actions={
                            <>
                              <MessageButton target={item.intelligence} />
                              {canMerge ? (
                                <Button
                                  variant="outline"
                                  size="sm"
                                  aria-label={t("ai.mergeAction", {
                                    name: item.intelligence.display_name,
                                  })}
                                  onClick={() => setMerging(item.intelligence)}
                                >
                                  <GitMergeIcon aria-hidden="true" />
                                  <span className="max-sm:hidden">{t("ai.mergeShort")}</span>
                                </Button>
                              ) : null}
                              <Button
                                variant="ghost"
                                size="icon-sm"
                                className="text-muted-foreground hover:text-destructive"
                                aria-label={t("ai.delete", {
                                  name: item.intelligence.display_name,
                                })}
                                title={t("ai.deleteShort")}
                                onClick={() => setDeleting(item.intelligence)}
                              >
                                <Trash2Icon aria-hidden="true" />
                              </Button>
                            </>
                          }
                        />
                      </li>
                    );
                  })}
                </ul>
              </Section>
            ) : null}

            {view.shared.length > 0 ? (
              <Section title={t("contacts.shared")} description={t("contacts.sharedBody")}>
                <ul className="divide-y border-y">
                  {view.shared.map((ai) => {
                    const item = live(ai);
                    return (
                      <li key={item.intelligence.id}>
                        <ContactRow
                          testId={`contact-${item.intelligence.id}`}
                          className="px-4 md:px-8"
                          intelligence={item.intelligence}
                          meta={<AIMeta ai={item} machine={item.machine} lastSeen />}
                          actions={<MessageButton target={item.intelligence} />}
                        />
                      </li>
                    );
                  })}
                </ul>
              </Section>
            ) : null}

            {view.people.length > 0 ? (
              <Section title={t("contacts.people")} description={t("contacts.peopleBody")}>
                <div className="border-t">
                  {view.people.map((group) => (
                    // A search and a plain list are different states of a group: start it afresh.
                    <PeopleGroup
                      key={`${group.connector.id}:${settled ? "search" : "list"}`}
                      group={group}
                      query={settled}
                    />
                  ))}
                </div>
              </Section>
            ) : null}

            {view.users.length > 0 ? (
              <Section title={t("contacts.users")}>
                <ul className="divide-y border-y">
                  {view.users.map((user) => (
                    <li key={user.id}>
                      <ContactRow
                        testId={`contact-${user.id}`}
                        className="px-4 md:px-8"
                        intelligence={user}
                        actions={<MessageButton target={user} />}
                      />
                    </li>
                  ))}
                </ul>
              </Section>
            ) : null}

            {text !== "" &&
            view.machines.length +
              view.unlinked.length +
              view.shared.length +
              view.users.length +
              view.people.length ===
              0 ? (
              <div className="space-y-3 px-4 py-10 text-sm text-muted-foreground md:px-8">
                <p role="status">{t("contacts.noMatch", { query: text })}</p>
                <Button variant="outline" size="sm" onClick={() => setFilter("")}>
                  {t("contacts.showAll")}
                </Button>
              </div>
            ) : null}
          </div>
        </>
      )}
      <ConnectAgentDialog open={connecting} onOpenChange={setConnecting} />
      <MergeAIDialog ai={merging} candidates={mergeCandidates} onClose={() => setMerging(null)} />
      <DeleteAIDialog ai={deleting} onClose={() => setDeleting(null)} />
    </>
  );
}

function FilterBox({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const { t } = useI18n();
  return (
    <search className="block border-b px-4 py-3 md:px-8">
      <div className="relative max-w-md">
        <SearchIcon
          className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground"
          aria-hidden="true"
        />
        <Input
          type="search"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape" && value) {
              e.preventDefault();
              onChange("");
            }
          }}
          placeholder={t("contacts.filterPlaceholder")}
          aria-label={t("contacts.filter")}
          autoComplete="off"
          className="h-11 pr-10 pl-8 md:h-9 [&::-webkit-search-cancel-button]:hidden"
        />
        {value ? (
          <button
            type="button"
            aria-label={t("contacts.filterClear")}
            onClick={() => onChange("")}
            className="absolute top-1/2 right-0.5 grid size-10 -translate-y-1/2 place-items-center rounded-md text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/70 md:size-8"
          >
            <XIcon className="size-4" aria-hidden="true" />
          </button>
        ) : null}
      </div>
    </search>
  );
}

function Section({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <section aria-labelledby={id} className="pt-7 first:pt-5">
      <div className="px-4 pb-2 md:px-8">
        <h2 id={id} className="text-lg font-semibold tracking-[-0.015em]">
          {title}
        </h2>
        {description ? (
          <p className="mt-0.5 max-w-[65ch] text-sm text-pretty text-muted-foreground">
            {description}
          </p>
        ) : null}
      </div>
      {children}
    </section>
  );
}

function ListSkeleton() {
  return (
    <ul aria-hidden="true" className="divide-y border-b">
      {[0, 1, 2].map((i) => (
        <li key={i} className="space-y-2 px-4 py-4 md:px-8">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-3 w-56 max-w-full" />
        </li>
      ))}
    </ul>
  );
}
