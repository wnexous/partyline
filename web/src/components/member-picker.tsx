import { Link } from "@tanstack/react-router";
import { PlusIcon, SearchIcon } from "lucide-react";
import { type ReactNode, useMemo, useState } from "react";
import { toast } from "sonner";
import { PickableMeta } from "@/components/contact-meta";
import {
  IntelligenceAvatar,
  IntelligenceName,
  useContacts,
  usePresenceStore,
} from "@/entities/intelligence";
import { useRoom } from "@/entities/room";
import { useAddMember } from "@/entities/room-member";
import { useOrgUsers } from "@/hooks/use-org-users";
import { type Pickable, pickableList } from "@/lib/contacts";
import { matchesTerms } from "@/lib/conversations";
import { withContactPresence } from "@/lib/presence";
import type { MemberKind, RoomMember } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { useAutoFocus } from "@/shared/lib/device";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/shared/ui/molecules/dialog";
import { Button } from "@/shared/ui/primitives/button";
import { Input } from "@/shared/ui/primitives/input";
import { Skeleton } from "@/shared/ui/primitives/skeleton";

/*
 * Adds members to a group (UI-10, CID-20), AIs and users in one list by name (AD-024). Only what
 * the user may add is offered (ACL-05): the AIs and the users of the group's organization (a group
 * is made of the members of its organization, ORG-03). While the organization's users cannot be
 * read, the instance's users are offered and the server refuses the ones who are not members.
 * Chats are never members (AD-015): a connection is an AI's own app, not a room participant.
 * External people are not members either: they join through the chat they write in.
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
  const room = useRoom(roomId);
  const add = useAddMember(roomId);
  const presence = usePresenceStore((s) => s.presence);
  const [query, setQuery] = useState("");
  const [adding, setAdding] = useState<string | null>(null);

  const taken = useMemo(() => new Set(members.map((m) => `${m.kind}:${m.member_id}`)), [members]);
  // A room that does not load leaves the AIs unscoped: the server decides who may join.
  const orgId = room.data?.org_id;
  const { users: orgUsers, loading: usersLoading } = useOrgUsers(orgId);
  const picks = useMemo(
    () => (contacts.data ? pickableList(contacts.data, orgId, orgUsers) : null),
    [contacts.data, orgId, orgUsers],
  );
  const candidates = (picks ?? []).filter(
    (p) => !taken.has(`${memberKindOf(p)}:${p.ref.id}`) && matchesTerms(p.haystack, query),
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

  const loading = contacts.isPending || room.isPending || usersLoading;
  const nothing = !loading && candidates.length === 0;

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
          <ul aria-label={t("picker.contacts")}>
            {candidates.map((p) => {
              const kind = memberKindOf(p);
              return (
                <PickableCandidate
                  key={p.ref.id}
                  testId={`candidate-${kind}-${p.ref.id}`}
                  pickable={p}
                  online={p.ai ? withContactPresence(p.ai, presence[p.ref.id]).online : false}
                  busy={adding === `${kind}:${p.ref.id}`}
                  onAdd={() => onAdd(kind, p.ref.id, p.ref.display_name)}
                />
              );
            })}
          </ul>
        )}
      </div>
    </>
  );
}

/** What the server calls the member a pick becomes: the pick of an AI joins as an agent. */
const memberKindOf = (p: Pickable): MemberKind => (p.ai ? "agent" : "user");

/** An AI or a user the room can take in: avatar, name and handle, and where it lives. */
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
