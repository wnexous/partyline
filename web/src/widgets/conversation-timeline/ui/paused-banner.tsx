import { PauseIcon } from "lucide-react";
import { toast } from "sonner";
import { useResumeRoom } from "@/entities/room";
import type { Room } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { Button } from "@/shared/ui/primitives/button";

/**
 * Tells that the loop guard paused the agents of a conversation (DLV-08). Resuming is up to whoever
 * the server says may (DLV-09): a group's managers and, since nobody manages a DM, the people who
 * can see it - its human participants and whoever manages one of its AIs.
 */
export function PausedBanner({ room }: { room: Room }) {
  const t = useT();
  const resume = useResumeRoom(room.id);
  const canResume = room.can_resume;
  return (
    <div
      role="status"
      className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-tape-strong/40 bg-muted px-4 py-2.5 text-sm md:px-6"
    >
      <PauseIcon className="size-4 shrink-0" aria-hidden="true" />
      <p className="min-w-0 flex-1">
        <span className="font-semibold">{t("room.pausedTitle")}</span>{" "}
        <span className="text-foreground/80">
          {canResume ? t("room.pausedBody") : t("room.pausedBodyReadOnly")}
        </span>
      </p>
      {canResume ? (
        <Button
          size="sm"
          disabled={resume.isPending}
          onClick={() =>
            resume.mutate(undefined, { onError: () => toast.error(t("room.resumeFailed")) })
          }
        >
          {t("room.resume")}
        </Button>
      ) : null}
    </div>
  );
}
