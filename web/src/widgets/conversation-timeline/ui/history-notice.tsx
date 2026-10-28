import { HistoryIcon } from "lucide-react";
import { platformName } from "@/entities/message";
import { useT } from "@/shared/i18n";

/**
 * A platform DM with a person is history (CID-21): the person answers on their platform, through
 * the connection that holds them, and partyline keeps the conversation only to be read.
 */
export function HistoryNotice({ platform }: { platform: string }) {
  const t = useT();
  return (
    <div
      role="note"
      data-testid="history-notice"
      className="flex items-start gap-2.5 border-t bg-muted/40 px-4 py-3 md:px-6"
    >
      <HistoryIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <div className="min-w-0 space-y-0.5">
        <p className="text-sm font-medium">
          {platform
            ? t("room.history", { platform: platformName(platform) })
            : t("room.historyUnknown")}
        </p>
        <p className="text-xs text-muted-foreground">{t("room.historyHint")}</p>
      </div>
    </div>
  );
}
