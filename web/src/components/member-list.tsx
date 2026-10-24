import { CircleUserRoundIcon, EllipsisIcon, MessageSquareIcon, XIcon } from "lucide-react";
import type { ReactNode } from "react";
import { toast } from "sonner";
import { IntelligenceAvatar } from "@/components/intelligence/intelligence-avatar";
import { IntelligenceName } from "@/components/intelligence/intelligence-name";
import { MemberSettings, memberSettingsSummary } from "@/components/member-settings";
import { StatusDot } from "@/components/status-dot";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useStartConversation } from "@/hooks/use-start-conversation";
import { useAddMember, useMe, useRemoveMember } from "@/lib/api/queries";
import type { IntelligenceRef, RoomMember } from "@/lib/api/types.gen";
import { useI18n } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";

/*
 * Room members (UI-10, UI-12, GRP-02): the room's intelligences in one list, in the order they
 * joined. An AI and a user look the same, with their avatar, name and @handle (PROF-09, AD-024);
 * what only an AI has (live presence, its host, its settings in the room) shows on its own row.
 * Chats are never members (CID-20, AD-015), so a chat row left in old data is not rendered: there
 * is no outbound switch and no platform badge any more. External people are not listed either:
 * they are not members.
 */

/** A chat member can still be in the stored data until the upgrade; it is not shown. */
const visibleMembers = (members: RoomMember[]) =>
  members.filter((m) => m.kind === "agent" || m.kind === "user");

export function MemberList({
  roomId,
  members,
  canManage,
}: {
  roomId: string;
  members: RoomMember[];
  canManage: boolean;
}) {
  const { t } = useI18n();
  const presence = useLiveStore((s) => s.presence);
  const remove = useRemoveMember(roomId);
  const add = useAddMember(roomId);
  const shown = visibleMembers(members);

  const onRemove = (m: RoomMember) => {
    const name = m.name;
    remove.mutate(m.id, {
      onSuccess: () =>
        toast(t("members.removed", { name }), {
          action: {
            label: t("members.undo"),
            onClick: () => add.mutate({ kind: m.kind, member_id: m.member_id }),
          },
        }),
      onError: () => toast.error(t("members.removeFailed", { name })),
    });
  };

  if (shown.length === 0) {
    return <p className="px-4 py-6 text-sm text-muted-foreground">{t("members.empty")}</p>;
  }

  return (
    <ul aria-label={t("members.title")} className="py-3">
      {shown.map((m) => (
        <MemberRow
          key={m.id}
          roomId={roomId}
          member={m}
          online={presence[m.member_id]?.online ?? m.online ?? false}
          canManage={canManage}
          onRemove={() => onRemove(m)}
        />
      ))}
    </ul>
  );
}

function MemberRow({
  roomId,
  member: m,
  online,
  canManage,
  onRemove,
}: {
  roomId: string;
  member: RoomMember;
  online: boolean;
  canManage: boolean;
  onRemove: () => void;
}) {
  const { t } = useI18n();
  const meId = useMe().data?.id;
  const name = m.name;
  // The profile of an AI or user member: avatar, name and @handle instead of an icon.
  const profile = m.kind === "agent" || m.kind === "user" ? m.intelligence : undefined;
  // PDM-04: nobody messages herself; a chat is no intelligence and is never shown here.
  const canMessage = !!profile && !(m.kind === "user" && m.member_id === meId);
  let meta: ReactNode = null;
  if (m.kind === "agent") {
    meta = (
      <>
        <StatusDot online={online} withLabel />
        {m.owner ? <span>{t("rooms.ownedBy", { username: m.owner })}</span> : null}
        {m.display_name ? (
          <span className="truncate">{t("memberSettings.as", { name: m.display_name })}</span>
        ) : null}
        {memberSettingsSummary(m).map((key) => (
          <span key={key}>{t(key)}</span>
        ))}
      </>
    );
  }

  return (
    <li
      data-testid={`member-${m.id}`}
      className="group/member relative flex items-start gap-2.5 px-4 py-2 hover:bg-accent/40"
    >
      <span className="mt-0.5 shrink-0">
        {profile ? (
          <IntelligenceAvatar intelligence={profile} decorative />
        ) : (
          <CircleUserRoundIcon className="size-4 text-muted-foreground" aria-hidden="true" />
        )}
      </span>
      <div className="min-w-0 flex-1">
        {profile ? (
          // The name opens the profile panel (PRF-01) and covers the row; the actions sit above it.
          <IntelligenceName
            intelligence={profile}
            viewerId={meId}
            showHandle
            profileLink
            stretch
            className="text-sm"
          />
        ) : (
          <p className="truncate text-sm font-medium">{name}</p>
        )}
        {meta ? (
          <p className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
            {meta}
          </p>
        ) : null}
      </div>
      <div className="relative flex shrink-0 items-start gap-2.5 empty:hidden">
        {canMessage && profile ? <MemberDmActions member={m} profile={profile} /> : null}
        {canManage && m.kind === "agent" ? <MemberSettings roomId={roomId} member={m} /> : null}
        {canManage ? (
          <Button
            variant="ghost"
            size="icon-xs"
            className="shrink-0 text-muted-foreground opacity-60 group-hover/member:opacity-100 focus-visible:opacity-100"
            aria-label={t("members.remove", { name })}
            onClick={onRemove}
          >
            <XIcon aria-hidden="true" />
          </Button>
        ) : null}
      </div>
    </li>
  );
}

/**
 * The actions of a member (PDM-01, PDM-02): "Message privately" opens the DM with an AI or a user,
 * the existing one or a new one, and goes to it. The row decides who gets it: an AI or another user
 * the viewer can see, never the viewer herself and never a person behind a platform DM (PDM-04).
 */
function MemberDmActions({ member, profile }: { member: RoomMember; profile: IntelligenceRef }) {
  const { t } = useI18n();
  const { start, pending } = useStartConversation({ onError: (message) => toast.error(message) });
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon-xs"
          className="shrink-0 text-muted-foreground opacity-60 group-hover/member:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100"
          aria-label={t("members.actions", { name: member.name })}
        >
          <EllipsisIcon aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuItem disabled={pending} onSelect={() => void start(profile)}>
          <MessageSquareIcon aria-hidden="true" />
          {t("members.messagePrivately")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
