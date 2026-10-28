import { useI18n } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { RelativeTime } from "@/shared/ui/atoms/relative-time";

/**
 * "Principal offline since 14:02": an AI whose principal session left while another session is live
 * (SESS-61). Its plain messages, DMs and platform events wait for the principal; the AI shows online
 * because a parallel session is live. Nothing without a moment (the principal is live, or the AI is
 * offline).
 */
export function PrincipalOfflineLine({
  since,
  online = true,
  testId,
  className,
}: {
  /** When the principal left, as an ISO string; absent when it is live or the AI is offline. */
  since: string | undefined;
  /** An offline AI has no principal to wait for, whatever the data still says. */
  online?: boolean;
  testId?: string;
  className?: string;
}) {
  const { t, locale } = useI18n();
  if (!since || !online) return null;
  return (
    <p
      data-testid={testId}
      className={cn("text-xs text-muted-foreground [overflow-wrap:anywhere]", className)}
    >
      <RelativeTime value={since} locale={locale}>
        {(time) => t("profile.principalOffline", { time })}
      </RelativeTime>
    </p>
  );
}
