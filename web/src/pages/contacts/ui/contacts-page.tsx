import { PlugIcon, SearchIcon, UsersRoundIcon, XIcon } from "lucide-react";
import { useMemo, useState } from "react";
import {
  AIMeta,
  ContactRow,
  filterOrgs,
  onlineCount,
  useContacts,
  usePresenceStore,
  withContactPresence,
} from "@/entities/intelligence";
import { ConnectAgentDialog } from "@/features/connect-agent";
import { MessageButton } from "@/features/start-conversation";
import type { AIContact, Contacts } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { matchesTerms } from "@/shared/lib/search";
import { EmptyState } from "@/shared/ui/molecules/empty-state";
import { ListSkeleton } from "@/shared/ui/molecules/list-skeleton";
import { PageHeader } from "@/shared/ui/molecules/page-header";
import { PageSection } from "@/shared/ui/molecules/page-section";
import { Button } from "@/shared/ui/primitives/button";
import { Input } from "@/shared/ui/primitives/input";

/*
 * Contacts (CONT-01, SR2-14): every intelligence the user can reach. The AIs of each of their
 * organizations and the other users; one filter narrows both. Each contact can be messaged (DM-01).
 * The pre-v0.5 platform people and the AIs no organization of theirs lists are not shown: their
 * earlier conversations stay readable, and an AI is merged from its own profile ("Merge into…").
 * The machines an AI runs on are not shown (AD-022).
 */

const isEmpty = (c: Contacts) =>
  c.org_ais.reduce((n, g) => n + g.ais.length, 0) + c.users.length === 0;

export function ContactsPage() {
  const { t } = useI18n();
  const contacts = useContacts();
  const presence = usePresenceStore((s) => s.presence);
  const [filter, setFilter] = useState("");
  const [connecting, setConnecting] = useState(false);
  const text = filter.trim();

  const data = contacts.data;
  const live = useMemo(
    () => (ai: AIContact) => withContactPresence(ai, presence[ai.intelligence.id]),
    [presence],
  );
  const view = useMemo(() => {
    if (!data) return null;
    return {
      // An organization with no AI to show is not listed.
      orgs: filterOrgs(data.org_ais, text).filter((g) => g.ais.length > 0),
      users: data.users.filter((u) => matchesTerms(`${u.display_name}\n@${u.handle}`, text)),
    };
  }, [data, text]);
  const ais = data ? data.org_ais.reduce((n, g) => n + g.ais.length, 0) : 0;

  // One way an AI joins: a session on a machine you run by hand. Both live behind the one button
  // that adds an AI.
  const connect = (
    <div className="flex flex-wrap justify-center gap-2">
      <Button onClick={() => setConnecting(true)}>
        <PlugIcon aria-hidden="true" />
        {t("connect.open")}
      </Button>
    </div>
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
            {view.orgs.map((group) => (
              <PageSection
                layout="list"
                key={group.org.id}
                title={group.org.name}
                description={t(
                  "contacts.onlineCount",
                  onlineCount(group.ais, (ai) => live(ai).online),
                )}
              >
                <ul className="divide-y border-y">
                  {group.ais.map((ai) => {
                    const item = live(ai);
                    return (
                      <li key={item.intelligence.id}>
                        <ContactRow
                          testId={`contact-${item.intelligence.id}`}
                          className="px-4 md:px-8"
                          intelligence={item.intelligence}
                          meta={<AIMeta ai={item} lastSeen />}
                          actions={<MessageButton target={item.intelligence} />}
                        />
                      </li>
                    );
                  })}
                </ul>
              </PageSection>
            ))}

            {text === "" && ais === 0 ? (
              <PageSection layout="list" title={t("contacts.myAIs")}>
                {/* The page header has the one button that connects an AI. */}
                <div className="space-y-1 px-4 pt-1 pb-2 text-sm text-muted-foreground md:px-8">
                  <p>{t("contacts.noAIs")}</p>
                  <p>{t("contacts.noAIsBody")}</p>
                </div>
              </PageSection>
            ) : null}

            {view.users.length > 0 ? (
              <PageSection layout="list" title={t("contacts.instance")}>
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
              </PageSection>
            ) : null}

            {text !== "" && view.orgs.length + view.users.length === 0 ? (
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
