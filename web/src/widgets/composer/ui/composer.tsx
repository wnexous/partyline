import { EyeIcon } from "lucide-react";
import { canPost } from "@/entities/room";
import type { Level, Message, RoomMember } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { ComposerForm } from "./composer-form";

export interface ComposerProps {
  roomId: string;
  roomName: string;
  level: Level;
  members?: RoomMember[];
  /** The message the next one answers (ACT-07); the room page holds it. */
  replyTo?: Message | null;
  /** Takes the reply back, or says it was sent: the room page drops `replyTo`. */
  onClearReply?: () => void;
  /** The message being edited in the box (EDT-10); null or absent while a new one is written. */
  editing?: Message | null;
  /** Leaves editing: after a save, on Escape and on Cancel. */
  onEditDone?: () => void;
}

export function Composer({
  roomId,
  roomName,
  level,
  members,
  replyTo,
  onClearReply,
  editing,
  onEditDone,
}: ComposerProps) {
  const { t } = useI18n();
  if (!canPost(level)) {
    return (
      <div className="flex items-center gap-2.5 border-t bg-muted/50 px-4 py-3 text-sm text-muted-foreground md:px-6">
        <EyeIcon className="size-4 shrink-0" aria-hidden="true" />
        <p>{t("composer.readOnly")}</p>
      </div>
    );
  }
  return (
    <ComposerForm
      roomId={roomId}
      roomName={roomName}
      members={members}
      replyTo={replyTo ?? null}
      onClearReply={onClearReply}
      editing={editing ?? null}
      onEditDone={onEditDone}
    />
  );
}
