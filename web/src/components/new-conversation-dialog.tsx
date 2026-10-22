import { type ReactNode, useMemo, useState } from "react";
import { PickableMeta } from "@/components/contact-meta";
import { FormAlert } from "@/components/form-fields";
import { IntelligenceAvatar } from "@/components/intelligence/intelligence-avatar";
import { IntelligenceName } from "@/components/intelligence/intelligence-name";
import { Button } from "@/components/ui/button";
import {
  Command,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { useStartConversation } from "@/hooks/use-start-conversation";
import { useContacts, usePeopleSearch } from "@/lib/api/queries";
import { type IntelligenceRef, MaxPeopleSearch } from "@/lib/api/types.gen";
import { type Pickable, pickables } from "@/lib/contacts";
import { matchesTerms } from "@/lib/conversations";
import { useT } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";
import { withContactPresence } from "@/lib/presence";

/*
 * "New conversation" (LAY-03, DM-01): pick any intelligence the user can see and open the one DM
 * between them, creating it when there is none. Users and AIs (the user's own, then the shared
 * ones) are listed as the contacts give them and filtered as the user types. External people are
 * too many to list: they are looked up on the server once the text has settled (DM-05).
 */

/** The server needs more than a letter to answer something useful. */
const MIN_PEOPLE_QUERY = 2;

/** Group headings are not in the uppercase label voice: it would turn "AIs" into "AIS". */
const GROUP = "[&_[cmdk-group-heading]]:px-2.5 [&_[cmdk-group-heading]]:font-semibold";

const withoutAt = (text: string) => text.replace(/^@+/, "");
const length = (text: string) => [...text].length;

export function NewConversationDialog({
  trigger,
  initialQuery = "",
}: {
  trigger: ReactNode;
  /** Text to start the search with, such as what the sidebar search could not find. */
  initialQuery?: string;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="flex max-h-[min(40rem,calc(100dvh-2rem))] flex-col gap-0 p-0 sm:max-w-lg">
        <DialogHeader className="border-b px-5 pt-5 pb-4 text-left">
          <DialogTitle>{t("newConversation.title")}</DialogTitle>
          <DialogDescription>{t("newConversation.body")}</DialogDescription>
        </DialogHeader>
        {/* Mounted only while open: the search starts as given and an old error is gone. */}
        {open ? <ContactPicker initialQuery={initialQuery} onDone={() => setOpen(false)} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function ContactPicker({ initialQuery, onDone }: { initialQuery: string; onDone: () => void }) {
  const t = useT();
  const contacts = useContacts();
  const presence = useLiveStore((s) => s.presence);
  const conversation = useStartConversation();
  const [query, setQuery] = useState(initialQuery);

  const text = query.trim();
  const settled = useDebouncedValue(text);

  const picks = useMemo(() => (contacts.data ? pickables(contacts.data) : null), [contacts.data]);
  const ais = useMemo(
    () => (picks?.ais ?? []).filter((p) => matchesTerms(p.haystack, text)),
    [picks, text],
  );
  const users = useMemo(
    () => (picks?.users ?? []).filter((p) => matchesTerms(p.haystack, text)),
    [picks, text],
  );

  // Only connectors that have seen someone are worth asking, and only about settled text.
  const connectorIds = useMemo(
    () => contacts.data?.people.filter((g) => g.count > 0).map((g) => g.connector.id) ?? [],
    [contacts.data],
  );
  const asked = [...withoutAt(settled)].slice(0, MaxPeopleSearch).join("");
  const wants = connectorIds.length > 0 && length(withoutAt(text)) >= MIN_PEOPLE_QUERY;
  const lookUp = connectorIds.length > 0 && length(asked) >= MIN_PEOPLE_QUERY;
  const found = usePeopleSearch(connectorIds, asked, lookUp);
  const people = lookUp ? found.people : [];
  const waiting = wants && (settled !== text || found.searching);

  const hasResults = ais.length + users.length + people.length > 0;

  const pick = async (target: IntelligenceRef) => {
    if (await conversation.start(target)) onDone();
  };

  return (
    <Command
      label={t("newConversation.search")}
      shouldFilter={false}
      loop
      className="min-h-0 flex-1 rounded-none bg-transparent"
    >
      <CommandInput
        value={query}
        onValueChange={setQuery}
        placeholder={t("newConversation.placeholder")}
        className="text-base md:text-sm"
      />
      <CommandList
        label={t("newConversation.results")}
        aria-busy={conversation.pending || undefined}
        className="max-h-none min-h-0 flex-1 px-1.5 py-1.5"
      >
        {ais.length > 0 ? (
          <CommandGroup heading={t("contacts.ais")} className={GROUP}>
            {ais.map((p) => (
              <ContactRow
                key={p.ref.id}
                pickable={p}
                online={p.ai ? withContactPresence(p.ai, presence[p.ref.id]).online : false}
                disabled={conversation.pending}
                onPick={pick}
              />
            ))}
          </CommandGroup>
        ) : null}
        {users.length > 0 ? (
          <CommandGroup heading={t("contacts.users")} className={GROUP}>
            {users.map((p) => (
              <ContactRow
                key={p.ref.id}
                pickable={p}
                online={false}
                disabled={conversation.pending}
                onPick={pick}
              />
            ))}
          </CommandGroup>
        ) : null}
        {people.length > 0 ? (
          <CommandGroup heading={t("contacts.people")} className={GROUP}>
            {people.map((person) => (
              <ContactRow
                key={person.id}
                pickable={{ ref: person, machineLabel: "", haystack: "" }}
                online={false}
                disabled={conversation.pending}
                onPick={pick}
              />
            ))}
          </CommandGroup>
        ) : null}
      </CommandList>

      <div className="shrink-0 space-y-2 px-5 empty:hidden">
        {conversation.error ? <FormAlert>{conversation.error}</FormAlert> : null}
      </div>

      {contacts.isPending ? (
        <div className="space-y-3 px-5 py-4" aria-hidden="true">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-4 w-56" />
          <Skeleton className="h-4 w-48" />
        </div>
      ) : contacts.isError && !contacts.data ? (
        <div role="alert" className="px-5 py-6 text-sm">
          <p className="text-destructive">{t("contacts.loadError")}</p>
          <Button
            variant="outline"
            size="sm"
            className="mt-3"
            onClick={() => void contacts.refetch()}
          >
            {t("common.retry")}
          </Button>
        </div>
      ) : (
        <div className="shrink-0 px-5 py-3 text-sm text-muted-foreground empty:hidden">
          {!hasResults && !waiting ? (
            <p role="status">
              {text ? t("newConversation.empty", { query: text }) : t("newConversation.none")}
            </p>
          ) : null}
          {waiting && !hasResults ? <p role="status">{t("newConversation.searching")}</p> : null}
          {found.failed && lookUp ? (
            <p role="alert" className="text-destructive">
              {t("newConversation.peopleError")}
            </p>
          ) : null}
          {connectorIds.length > 0 && !wants ? (
            <p className="text-xs">{t("newConversation.peopleHint", { min: MIN_PEOPLE_QUERY })}</p>
          ) : null}
        </div>
      )}
    </Command>
  );
}

function ContactRow({
  pickable,
  online,
  disabled,
  onPick,
}: {
  pickable: Pickable;
  online: boolean;
  disabled: boolean;
  onPick: (target: IntelligenceRef) => void;
}) {
  const { ref } = pickable;
  return (
    <CommandItem
      value={ref.id}
      data-testid={`contact-${ref.id}`}
      disabled={disabled}
      onSelect={() => onPick(ref)}
      className="items-start gap-3 rounded-md px-2.5 py-2 data-[selected=true]:bg-tape/55 data-[selected=true]:text-tape-foreground dark:data-[selected=true]:bg-tape/14 dark:data-[selected=true]:text-tape"
    >
      <IntelligenceAvatar intelligence={ref} decorative />
      <div className="min-w-0 flex-1">
        <IntelligenceName intelligence={ref} showHandle />
        <p className="mt-0.5 flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
          <PickableMeta pickable={pickable} online={online} />
        </p>
      </div>
    </CommandItem>
  );
}
