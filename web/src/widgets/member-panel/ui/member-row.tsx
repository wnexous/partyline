import { MessageSquareIcon, XIcon } from "lucide-react";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { MemberSettings } from "@/components/member-settings";
import {
  AiStatusLine,
  canMessage,
  IntelligenceRow,
  SessionList,
  useIntelligence,
  useIsOnline,
} from "@/entities/intelligence";
import type { PersonRow } from "@/entities/room-member";
import { memberSettingsSummary } from "@/entities/room-member";
import { useStartConversation } from "@/features/start-conversation";
import { useI18n } from "@/shared/i18n";
import { useTouchFirst } from "@/shared/lib/device";
import { StatusDot } from "@/shared/ui/atoms/status-dot";
import { ActionMenu } from "@/shared/ui/molecules/action-menu";
import { RowMenuButton } from "@/shared/ui/molecules/row-menu-button";
import { Button } from "@/shared/ui/primitives/button";

/**
 * What a group can do with a member row: its settings, and taking the member out. A DM has none of
 * it (DM-02): its row is read-only.
 */
export interface MemberRowGroup {
  roomId: string;
  canManage: boolean;
  onRemove: () => void;
}

/**
 * One person of a conversation's side panel, in a group or in a DM (UI-10, UI-12, DM-02): avatar,
 * name and @handle; for an AI, whether it is online and, while it is online with parallel sessions,
 * which ones (SESS-44); in a group also the owner, the room settings and the actions.
 */
export function MemberRow({
  row,
  viewerId,
  group,
}: {
  row: PersonRow;
  viewerId: string | undefined;
  /** Set for a group's member; absent for a DM participant. */
  group?: MemberRowGroup;
}) {
  const { t } = useI18n();
  const member = row.member;
  const isAI = row.kind === "ai";
  const online = useIsOnline(row.id, row.online);
  const testPrefix = member ? "member" : "participant";

  let meta = null;
  let sessions = null;
  if (isAI && member) {
    // A group's member comes with its owner, settings and sessions.
    meta = (
      <>
        <StatusDot
          online={online}
          label={online ? t("presence.online") : t("presence.offline")}
          withLabel
        />
        {member.owner ? <span>{t("rooms.ownedBy", { username: member.owner })}</span> : null}
        {member.display_name ? (
          <span className="[overflow-wrap:anywhere]">
            {t("memberSettings.as", { name: member.display_name })}
          </span>
        ) : null}
        {memberSettingsSummary(member).map((key) => (
          <span key={key}>{t(key)}</span>
        ))}
      </>
    );
    sessions = (
      <SessionList
        sessions={row.sessions}
        online={online}
        testId={`member-sessions-${row.rowId}`}
      />
    );
  } else if (isAI) {
    // A DM participant is only a profile: its status and sessions come from the AI's own profile.
    meta = <AiStatusLine id={row.id} className="flex" />;
    sessions = <ParticipantSessions id={row.id} testId={`participant-sessions-${row.rowId}`} />;
  }

  // PDM-04: nobody messages herself.
  const canMessageRow =
    !!member && canMessage(viewerId ?? "", { id: row.id, role: row.kind === "ai" ? "ai" : "user" });
  const actions = group ? (
    <>
      {canMessageRow ? <MessagePrivately row={row} /> : null}
      {group.canManage && member && isAI ? (
        <MemberSettings roomId={group.roomId} member={member} />
      ) : null}
      {group.canManage ? (
        <Button
          variant="ghost"
          size="icon-xs"
          className="shrink-0 text-muted-foreground opacity-60 group-hover/row:opacity-100 focus-visible:opacity-100"
          aria-label={t("members.remove", { name: row.name })}
          onClick={group.onRemove}
        >
          <XIcon aria-hidden="true" />
        </Button>
      ) : null}
    </>
  ) : null;

  return (
    <IntelligenceRow
      testId={`${testPrefix}-${row.rowId}`}
      intelligence={row.intelligence}
      viewerId={viewerId}
      meta={meta}
      sessions={sessions}
      actions={actions}
    />
  );
}

/** The sessions of a DM's AI, read from its profile since a participant carries none. */
function ParticipantSessions({ id, testId }: { id: string; testId: string }) {
  const ai = useIntelligence(id).data?.ai;
  const online = useIsOnline(id, ai?.online);
  return <SessionList sessions={ai?.sessions} online={online} testId={testId} />;
}

/**
 * "Message privately" (PDM-01, PDM-02): opens the DM with an AI or a user, the existing one or a
 * new one, and goes to it. Never offered for a person behind a platform DM or for the viewer.
 */
function MessagePrivately({ row }: { row: PersonRow }) {
  const { t } = useI18n();
  const touch = useTouchFirst();
  const { start, pending } = useStartConversation({ onError: (message) => toast.error(message) });
  const [menu, setMenu] = useState<{ at: { x: number; y: number; align: "end" } } | null>(null);
  // The last place the menu hung from stays drawn while it fades out, and takes focus back.
  const last = useRef<{ x: number; y: number; align: "end" }>({ x: 0, y: 0, align: "end" });
  const opener = useRef<HTMLElement | null>(null);
  if (menu) last.current = menu.at;
  const label = t("members.actions", { name: row.name });
  return (
    <>
      <RowMenuButton
        label={label}
        expanded={menu !== null}
        onOpen={(at, button) => {
          opener.current = button;
          setMenu({ at });
        }}
        className="shrink-0 opacity-60 group-hover/row:opacity-100 aria-expanded:opacity-100"
      />
      <ActionMenu
        open={menu !== null}
        onClose={() => setMenu(null)}
        touch={touch}
        title={row.name}
        label={label}
        at={last.current}
        actions={[
          {
            id: "message",
            label: t("members.messagePrivately"),
            icon: MessageSquareIcon,
            disabled: pending,
            onSelect: () => void start(row.intelligence),
          },
        ]}
        onCloseFocus={(event) => {
          event.preventDefault();
          opener.current?.focus({ preventScroll: true });
        }}
      />
    </>
  );
}
