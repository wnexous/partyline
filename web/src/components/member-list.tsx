import { BotIcon, MessageCircleIcon, UserIcon, XIcon } from "lucide-react";
import { type ReactNode, useId } from "react";
import { toast } from "sonner";
import { MemberSettings, memberSettingsSummary } from "@/components/member-settings";
import { platformLabel } from "@/components/message-item";
import { StatusDot } from "@/components/status-dot";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { useAddMember, useRemoveMember, useUpdateMember } from "@/lib/api/queries";
import type { MemberKind, RoomMember } from "@/lib/api/types.gen";
import { type MessageKey, useI18n } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";

/*
 * Room members (UI-10, UI-12): everything wired to the room, shown plainly. Agents show live
 * presence; chats show whether agent and people posts go out to them (outbound, DLV-03).
 */

const GROUPS: { kind: MemberKind; label: MessageKey }[] = [
  { kind: "agent", label: "members.agents" },
  { kind: "chat", label: "members.chats" },
  { kind: "user", label: "members.people" },
];

const CHAT_KIND: Record<string, MessageKey> = {
  group: "members.chatKind.group",
  dm: "members.chatKind.dm",
  channel: "members.chatKind.channel",
};

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
  const update = useUpdateMember(roomId);
  const remove = useRemoveMember(roomId);
  const add = useAddMember(roomId);

  const onRemove = (m: RoomMember) => {
    remove.mutate(m.id, {
      onSuccess: () =>
        toast(t("members.removed", { name: m.name }), {
          action: {
            label: t("members.undo"),
            onClick: () =>
              add.mutate({ kind: m.kind, member_id: m.member_id, outbound: m.outbound }),
          },
        }),
      onError: () => toast.error(t("members.removeFailed", { name: m.name })),
    });
  };

  const onOutbound = (m: RoomMember, outbound: boolean) => {
    update.mutate(
      { memberId: m.id, outbound },
      { onError: () => toast.error(t("members.updateFailed", { name: m.name })) },
    );
  };

  if (members.length === 0) {
    return <p className="px-4 py-6 text-sm text-muted-foreground">{t("members.empty")}</p>;
  }

  return (
    <div className="space-y-5 py-3">
      {GROUPS.map(({ kind, label }) => {
        const list = members.filter((m) => m.kind === kind);
        if (list.length === 0) return null;
        return (
          <section key={kind} aria-label={t(label)}>
            <h3 className="flex items-baseline gap-2 px-4 pb-1.5 label-caps text-[0.68rem] text-muted-foreground">
              {t(label)}
              <span className="font-mono tabular">{list.length}</span>
            </h3>
            <ul>
              {list.map((m) => (
                <MemberRow
                  key={m.id}
                  roomId={roomId}
                  member={m}
                  online={presence[m.member_id]?.online ?? m.online ?? false}
                  canManage={canManage}
                  onRemove={() => onRemove(m)}
                  onOutbound={(v) => onOutbound(m, v)}
                />
              ))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}

function MemberRow({
  roomId,
  member: m,
  online,
  canManage,
  onRemove,
  onOutbound,
}: {
  roomId: string;
  member: RoomMember;
  online: boolean;
  canManage: boolean;
  onRemove: () => void;
  onOutbound: (outbound: boolean) => void;
}) {
  const { t } = useI18n();
  const switchId = useId();
  let icon: ReactNode;
  let meta: ReactNode = null;
  if (m.kind === "agent") {
    icon = <BotIcon className="size-4 text-muted-foreground" aria-hidden="true" />;
    meta = (
      <>
        <StatusDot online={online} withLabel />
        {m.owner ? <span>@{m.owner}</span> : null}
        {m.display_name ? (
          <span className="truncate">{t("memberSettings.as", { name: m.display_name })}</span>
        ) : null}
        {memberSettingsSummary(m).map((key) => (
          <span key={key}>{t(key)}</span>
        ))}
      </>
    );
  } else if (m.kind === "chat") {
    icon = <MessageCircleIcon className="size-4 text-info" aria-hidden="true" />;
    const kind = m.chat_kind ? CHAT_KIND[m.chat_kind] : undefined;
    meta = (
      <span>
        {platformLabel(m.connector_kind)}
        {kind ? ` · ${t(kind)}` : ""}
      </span>
    );
  } else {
    icon = <UserIcon className="size-4 text-muted-foreground" aria-hidden="true" />;
  }

  return (
    <li
      data-testid={`member-${m.id}`}
      className="group/member flex items-start gap-2.5 px-4 py-2 hover:bg-accent/40"
    >
      <span className="mt-0.5 shrink-0">{icon}</span>
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium">{m.name}</p>
        {meta ? (
          <p className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
            {meta}
          </p>
        ) : null}
        {m.kind === "chat" ? (
          canManage ? (
            <div className="mt-1.5 flex items-center gap-2 text-xs text-muted-foreground">
              <Switch
                id={switchId}
                size="sm"
                checked={m.outbound}
                onCheckedChange={onOutbound}
                aria-label={t("members.outboundFor", { name: m.name })}
              />
              <label htmlFor={switchId} aria-hidden="true">
                {t("members.outbound")}
              </label>
            </div>
          ) : (
            <p className="mt-1 text-xs text-muted-foreground">
              {m.outbound ? t("members.outboundOn") : t("members.outboundOff")}
            </p>
          )
        ) : null}
      </div>
      {canManage && m.kind === "agent" ? <MemberSettings roomId={roomId} member={m} /> : null}
      {canManage ? (
        <Button
          variant="ghost"
          size="icon-xs"
          className="shrink-0 text-muted-foreground opacity-60 group-hover/member:opacity-100 focus-visible:opacity-100"
          aria-label={t("members.remove", { name: m.name })}
          onClick={onRemove}
        >
          <XIcon aria-hidden="true" />
        </Button>
      ) : null}
    </li>
  );
}
