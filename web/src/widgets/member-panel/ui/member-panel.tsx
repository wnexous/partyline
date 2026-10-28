import type { ReactNode } from "react";
import { toast } from "sonner";
import { type PersonRow, toPersonRow } from "@/entities/room-member";
import { useAddMember, useMe, useRemoveMember } from "@/lib/api/queries";
import type { IntelligenceRef, RoomMember } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { MemberRow } from "./member-row";

/*
 * The people of a conversation, in its side panel (UI-10, UI-12, GRP-02, DM-02): a group lists its
 * room members, in the order they joined, with their settings and the actions; a DM lists its two
 * participants and offers no way to change them. Both read through one adapter into one row, with
 * the same loading and empty states. An AI and a user look the same, with avatar, name and @handle;
 * what only an AI has (presence, host, sessions) shows on its own row (AD-024). Chats are never
 * members (CID-20, AD-015), so a chat row left in old data is not rendered.
 */
export function MemberPanel({
  roomId,
  members,
  participants,
  canManage,
  loading,
}: {
  roomId: string;
  /** A group's room members. */
  members: RoomMember[];
  /** Set for a DM: its participants replace the members. */
  participants?: IntelligenceRef[];
  canManage: boolean;
  loading: boolean;
}) {
  const { t } = useI18n();
  const viewerId = useMe().data?.id;
  const remove = useRemoveMember(roomId);
  const add = useAddMember(roomId);

  const isDM = participants !== undefined;
  const rows = (participants ?? members)
    .map((source) => toPersonRow(source))
    .filter((row): row is PersonRow => row !== null);

  const onRemove = (member: RoomMember) => {
    const name = member.name;
    remove.mutate(member.id, {
      onSuccess: () =>
        toast(t("members.removed", { name }), {
          action: {
            label: t("members.undo"),
            onClick: () => add.mutate({ kind: member.kind, member_id: member.member_id }),
          },
        }),
      onError: () => toast.error(t("members.removeFailed", { name })),
    });
  };

  let body: ReactNode;
  if (loading) {
    body = (
      <div className="space-y-3 p-4" aria-hidden="true">
        <Skeleton className="h-4 w-36" />
        <Skeleton className="h-4 w-44" />
      </div>
    );
  } else if (rows.length === 0) {
    body = (
      <p className="px-4 py-6 text-sm text-muted-foreground">
        {t(isDM ? "participants.empty" : "members.empty")}
      </p>
    );
  } else {
    body = (
      <ul aria-label={t(isDM ? "participants.title" : "members.title")} className="py-3">
        {rows.map((row) => {
          const member = row.member;
          return (
            <MemberRow
              key={row.rowId}
              row={row}
              viewerId={viewerId}
              group={
                isDM || !member
                  ? undefined
                  : { roomId, canManage, onRemove: () => onRemove(member) }
              }
            />
          );
        })}
      </ul>
    );
  }
  return <div className="min-h-0 flex-1 overflow-y-auto">{body}</div>;
}
