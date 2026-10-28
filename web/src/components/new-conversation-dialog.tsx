import { type ReactNode, useMemo, useState } from "react";
import { PickableMeta } from "@/components/contact-meta";
import { FormAlert } from "@/components/form-fields";
import { Button } from "@/shared/ui/primitives/button";
import { Command, CommandInput, CommandItem, CommandList } from "@/shared/ui/primitives/command";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/shared/ui/primitives/dialog";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { IntelligenceAvatar, IntelligenceName } from "@/entities/intelligence";
import { useStartConversation } from "@/hooks/use-start-conversation";
import { useContacts } from "@/lib/api/queries";
import type { IntelligenceRef } from "@/lib/api/types.gen";
import { type Pickable, pickableList } from "@/lib/contacts";
import { matchesTerms } from "@/lib/conversations";
import { useT } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";
import { withContactPresence } from "@/lib/presence";
import { isCoarsePointer } from "@/shared/lib/device";

/*
 * "New conversation" (LAY-03, DM-01): pick an intelligence the user can see and open the one DM
 * between them, creating it when there is none. AIs and users are one list by name (AD-024),
 * filtered as the user types. External people are not offered: a platform DM with a person is
 * history and the person answers on their platform (CID-21, CID-22; DM-05 retired).
 */

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
      <DialogContent className="flex max-h-[min(40rem,calc(100dvh-2rem))] flex-col gap-0 p-0 sm:max-w-lg" closeLabel={t("common.close")} touchFirst={isCoarsePointer()}>
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

  const picks = useMemo(
    () => (contacts.data ? pickableList(contacts.data) : null),
    [contacts.data],
  );
  const found = useMemo(
    () => (picks ?? []).filter((p) => matchesTerms(p.haystack, text)),
    [picks, text],
  );

  const hasResults = found.length > 0;

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
        {found.map((p) => (
          <ContactRow
            key={p.ref.id}
            pickable={p}
            online={p.ai ? withContactPresence(p.ai, presence[p.ref.id]).online : false}
            disabled={conversation.pending}
            onPick={pick}
          />
        ))}
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
          {!hasResults ? (
            <p role="status">
              {text ? t("newConversation.empty", { query: text }) : t("newConversation.none")}
            </p>
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
      className="items-start gap-3 rounded-md px-2.5 py-2 data-[selected=true]:bg-tape/60 data-[selected=true]:text-tape-foreground"
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
