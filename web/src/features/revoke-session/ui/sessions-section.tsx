import { useId } from "react";
import { toast } from "sonner";
import { useRevokeSession, useSessions } from "@/entities/account";
import type { Session } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { RelativeTime } from "@/shared/ui/atoms/relative-time";
import { PageSection as Section } from "@/shared/ui/molecules/page-section";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { describeDevice } from "../lib/describe-device";

export function SessionsSection() {
  const { t, locale } = useI18n();
  const id = useId();
  const sessions = useSessions();
  const revoke = useRevokeSession();

  const logOut = (s: Session, device: string) =>
    revoke.mutate(s.id, {
      onSuccess: () => toast(t("account.sessionRevoked", { device })),
      onError: () => toast.error(t("account.sessionRevokeFailed")),
    });

  return (
    <Section
      id={`${id}-sessions`}
      title={t("account.sessions")}
      description={t("account.sessionsBody")}
    >
      {sessions.isPending ? (
        <Skeleton className="h-16 w-full" />
      ) : sessions.isError ? (
        <p role="alert" className="text-sm text-destructive">
          {t("account.sessionsError")}
        </p>
      ) : (
        <ul aria-label={t("account.sessions")} className="divide-y rounded-md border">
          {(sessions.data ?? []).map((s) => {
            const device = describeDevice(t, s.user_agent);
            return (
              <li key={s.id} className="flex items-center gap-3 px-3 py-2.5">
                <div className="min-w-0 flex-1">
                  <p className="flex items-center gap-2 text-sm font-medium">
                    <span className="truncate">{device}</span>
                    {s.current ? (
                      <span className="shrink-0 rounded-[3px] bg-tape px-1.5 py-px text-[0.7rem] font-semibold text-tape-foreground">
                        {t("account.thisDevice")}
                      </span>
                    ) : null}
                  </p>
                  <p className="flex flex-wrap gap-x-3 text-xs text-muted-foreground">
                    <span className="font-mono">{s.ip}</span>
                    <RelativeTime value={s.last_seen_at} locale={locale}>
                      {(time) => t("account.lastActive", { time })}
                    </RelativeTime>
                  </p>
                </div>
                {s.current ? null : (
                  <Button
                    variant="outline"
                    size="sm"
                    aria-label={t("account.sessionRevoke", { device })}
                    disabled={revoke.isPending}
                    onClick={() => logOut(s, device)}
                  >
                    {t("shell.logout")}
                  </Button>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </Section>
  );
}
