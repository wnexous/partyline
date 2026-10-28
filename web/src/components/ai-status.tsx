import { appLabelOf, useIntelligence, useIsOnline } from "@/entities/intelligence";
import { useT } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { StatusDot } from "@/shared/ui/atoms/status-dot";

/**
 * Whether an AI is online and the app it runs in (Claude Code, OpenCode…), from its profile and
 * the live presence frames. Nothing while the profile loads or when the caller cannot see it.
 */
export function AiStatusLine({
  id,
  className,
  appClassName,
}: {
  id: string;
  className?: string;
  /** Classes of the app name, so a tight header can hide it. */
  appClassName?: string;
}) {
  const t = useT();
  const profile = useIntelligence(id);
  const ai = profile.data?.ai;
  const online = useIsOnline(id, ai?.online);
  if (!ai) return null;
  const app = appLabelOf({ app: ai.app, apps: profile.data?.apps });
  return (
    <span
      className={cn(
        "inline-flex items-center gap-x-2 text-xs whitespace-nowrap text-muted-foreground",
        className,
      )}
    >
      <StatusDot
        online={online}
        label={online ? t("presence.online") : t("presence.offline")}
        withLabel
      />
      {app ? <span className={appClassName}>{app}</span> : null}
    </span>
  );
}
