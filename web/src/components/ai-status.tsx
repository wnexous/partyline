import { StatusDot } from "@/components/status-dot";
import { harnessLabel } from "@/lib/agents";
import { useIntelligence } from "@/lib/api/queries";
import { useLiveStore } from "@/lib/live/store";
import { cn } from "@/lib/utils";

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
  const profile = useIntelligence(id);
  const live = useLiveStore((s) => s.presence[id]);
  const ai = profile.data?.ai;
  if (!ai) return null;
  const app = profile.data?.apps[0]?.label || harnessLabel(ai.app);
  return (
    <span
      className={cn(
        "inline-flex items-center gap-x-2 text-xs whitespace-nowrap text-muted-foreground",
        className,
      )}
    >
      <StatusDot online={live?.online ?? ai.online} withLabel />
      {app ? <span className={appClassName}>{app}</span> : null}
    </span>
  );
}
