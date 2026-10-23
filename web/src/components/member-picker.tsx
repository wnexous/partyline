import { Link } from "@tanstack/react-router";
import { PlusIcon, SearchIcon } from "lucide-react";
import { type ReactNode, useMemo, useState } from "react";
import { toast } from "sonner";
import { PickableMeta } from "@/components/contact-meta";
import { IntelligenceAvatar } from "@/components/intelligence/intelligence-avatar";
import { IntelligenceName } from "@/components/intelligence/intelligence-name";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { useAutoFocus } from "@/hooks/use-auto-focus";
import { useAddMember, useContacts } from "@/lib/api/queries";
import type { MemberKind, RoomMember } from "@/lib/api/types.gen";
import { type Pickable, pickables } from "@/lib/contacts";
import { matchesTerms } from "@/lib/conversations";
import { useI18n } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";
import { withContactPresence } from "@/lib/presence";

/*
 * Adds users and AIs to a group (UI-10, CID-20). Only what the user may add is offered (ACL-05):
 * their own AIs and the AIs shared with them, and the other users of the instance. Chats are never
 * members (AD-015): a connection is an AI's own app, not a room participant. External people are
 * not members either: they join through the chat they write in.
 */

export function MemberPicker({
  roomId,
  members,
  trigger,
}: {
  roomId: string;
  members: RoomMember[];
  trigger: ReactNode;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="flex max-h-[min(40rem,calc(100dvh-2rem))] flex-col gap-0 p-0 sm:max-w-lg">
        <DialogHeader className="border-b px-5 pt-5 pb-4 text-left">
          <DialogTitle>{t("picker.title")}</DialogTitle>
          <DialogDescription>{t("picker.body")}</DialogDescription>
        </DialogHeader>
        {open ? <PickerBody roomId={roomId} members={members} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function PickerBody({ roomId, members }: { roomId: string; members: RoomMember[] }) {
  const { t } = useI18n();
  const autoFocus = useAutoFocus();
  const contacts = useContacts();
  const add = useAddMember(roomId);
  const presence = useLiveStore((s) => s.presence);
  const [query, setQuery] = useState("");
  const [adding, setAdding] = useState<string | null>(null);

  const taken = useMemo(() => new Set(members.map((m) => `${m.kind}:${m.member_id}`)), [members]);
  const picks = useMemo(() => (contacts.data ? pickables(contacts.data) : null), [contacts.data]);
  const agentList = (picks?.ais ?? []).filter(
    (p) => !taken.has(`agent:${p.ref.id}`) && matchesTerms(p.haystack, query),
  );
  const userList = (picks?.users ?? []).filter(
    (p) => !taken.has(`user:${p.ref.id}`) && matchesTerms(p.haystack, query),
  );
  const onAdd = (kind: MemberKind, id: string, name: string) => {
    setAdding(`${kind}:${id}`);
    add.mutate(
      { kind, member_id: id },
      {
        onSuccess: () => toast.success(t("picker.added", { name })),
        onError: () => toast.error(t("picker.addFailed", { name })),
        onSettled: () => setAdding(null),
      },
    );
  };

  const loading = contacts.isPending;
  const nothing = !loading && agentList.length === 0 && userList.length === 0;

  return (
    <>
      <div className="relative border-b px-5 py-3">
        <SearchIcon
          className="pointer-events-none absolute top-1/2 left-8 size-4 -translate-y-1/2 text-muted-foreground"
          aria-hidden="true"
        />
        <Input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("picker.filterPlaceholder")}
          aria-label={t("picker.filter")}
          className="pl-9"
          autoFocus={autoFocus}
        />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto py-2">
        {loading ? (
          <div className="space-y-3 px-5 py-3" aria-hidden="true">
            <Skeleton className="h-4 w-40" />
            <Skeleton className="h-4 w-56" />
            <Skeleton className="h-4 w-48" />
          </div>
        ) : nothing ? (
          <div className="px-5 py-8 text-center text-sm text-muted-foreground">
            <p>{query ? t("picker.noMatch") : t("picker.nothing")}</p>
            {!query ? (
              <p className="mt-3 flex justify-center gap-4">
                <Link to="/contacts" className="font-medium text-foreground underline">
                  {t("picker.connectAgent")}
                </Link>
              </p>
            ) : null}
          </div>
        ) : (
          <>
            {agentList.length > 0 ? (
              <CandidateGroup label={t("contacts.ais")}>
                {agentList.map((p) => (
                  <PickableCandidate
                    key={p.ref.id}
                    testId={`candidate-agent-${p.ref.id}`}
                    pickable={p}
                    online={p.ai ? withContactPresence(p.ai, presence[p.ref.id]).online : false}
                    busy={adding === `agent:${p.ref.id}`}
                    onAdd={() => onAdd("agent", p.ref.id, p.ref.display_name)}
                  />
                ))}
              </CandidateGroup>
            ) : null}
            {userList.length > 0 ? (
              <CandidateGroup label={t("contacts.users")}>
                {userList.map((p) => (
                  <PickableCandidate
                    key={p.ref.id}
                    testId={`candidate-user-${p.ref.id}`}
                    pickable={p}
                    online={false}
                    busy={adding === `user:${p.ref.id}`}
                    onAdd={() => onAdd("user", p.ref.id, p.ref.display_name)}
                  />
                ))}
              </CandidateGroup>
            ) : null}
          </>
        )}
      </div>
    </>
  );
}

function CandidateGroup({ label, children }: { label: string; children: ReactNode }) {
  return (
    <section aria-label={label} className="py-1">
      <h3 className="px-5 pt-2 pb-1 text-xs font-semibold text-muted-foreground">{label}</h3>
      <ul>{children}</ul>
    </section>
  );
}

/** An AI or a user the room can take in: avatar, name with its badge and handle, and where it lives. */
function PickableCandidate({
  testId,
  pickable,
  online,
  busy,
  onAdd,
}: {
  testId: string;
  pickable: Pickable;
  online: boolean;
  busy: boolean;
  onAdd: () => void;
}) {
  return (
    <Candidate
      testId={testId}
      icon={<IntelligenceAvatar intelligence={pickable.ref} decorative />}
      title={
        // The name opens the profile panel (PRF-01) and covers the row, the Add button stays above.
        <IntelligenceName
          intelligence={pickable.ref}
          showHandle
          profileLink
          stretch
          className="text-sm"
        />
      }
      name={pickable.ref.display_name}
      meta={<PickableMeta pickable={pickable} online={online} />}
      busy={busy}
      onAdd={onAdd}
    />
  );
}

function Candidate({
  testId,
  icon,
  title,
  name,
  meta,
  busy,
  onAdd,
}: {
  testId: string;
  icon: ReactNode;
  /** What the row shows as its name. */
  title: ReactNode;
  /** The plain name, for the add button's label. */
  name: string;
  meta: ReactNode;
  busy: boolean;
  onAdd: () => void;
}) {
  const { t } = useI18n();
  return (
    <li
      data-testid={testId}
      className="relative flex items-center gap-3 px-5 py-2 hover:bg-accent/40"
    >
      <span className="shrink-0">{icon}</span>
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium">{title}</p>
        <p className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">{meta}</p>
      </div>
      <Button
        size="sm"
        variant="outline"
        className="relative"
        disabled={busy}
        aria-label={t("picker.add", { name })}
        onClick={onAdd}
      >
        <PlusIcon aria-hidden="true" />
        {t("picker.addShort")}
      </Button>
    </li>
  );
}
