import { ContainerIcon, ServerIcon } from "lucide-react";
import type { ReactNode } from "react";
import type { Runner } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { RelativeTime } from "@/shared/ui/atoms/relative-time";
import { StatusDot } from "@/shared/ui/atoms/status-dot";

/**
 * One of the owner's servers (CA-01): whether it is connected, its name and machine, whether
 * Docker is usable (its version, or why it is not), how many container AIs it runs and when it was
 * last seen. `actions` holds what the caller may do to it (rename, remove). The row wraps instead
 * of overflowing at 360 px with a large font.
 */
export function RunnerRow({
  runner,
  actions,
  testId,
  className,
}: {
  runner: Runner;
  actions?: ReactNode;
  testId?: string;
  className?: string;
}) {
  const { t, locale } = useI18n();
  const containers =
    runner.container_count === 0
      ? t("servers.noContainers")
      : t(runner.container_count === 1 ? "servers.oneContainer" : "servers.manyContainers", {
          count: runner.container_count,
        });
  return (
    <li
      data-testid={testId}
      className={cn("group/row relative flex items-start gap-2.5 px-3 py-3", className)}
    >
      <span
        aria-hidden="true"
        className="mt-0.5 grid size-9 shrink-0 place-items-center rounded-md border bg-muted/45 text-muted-foreground"
      >
        <ServerIcon className="size-4" />
      </span>
      <div className="min-w-0 flex-1 space-y-1">
        <p className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="min-w-0 font-medium [overflow-wrap:anywhere]">{runner.name}</span>
          <StatusDot
            online={runner.online}
            label={runner.online ? t("presence.online") : t("presence.offline")}
            withLabel
          />
        </p>
        <p className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground">
          <span translate="no" className="font-mono [overflow-wrap:anywhere]">
            {runner.machine}
          </span>
          <span aria-hidden="true">·</span>
          <span>{containers}</span>
        </p>
        <p className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs">
          {runner.docker_error ? (
            <span className="inline-flex items-center gap-1 text-destructive [overflow-wrap:anywhere]">
              <ContainerIcon aria-hidden="true" className="size-3.5 shrink-0" />
              {t("servers.dockerError", { reason: runner.docker_error })}
            </span>
          ) : runner.docker_version ? (
            <span className="inline-flex items-center gap-1 text-muted-foreground">
              <ContainerIcon aria-hidden="true" className="size-3.5 shrink-0" />
              {t("servers.dockerVersion", { version: runner.docker_version })}
            </span>
          ) : (
            <span className="text-muted-foreground">{t("servers.dockerUnknown")}</span>
          )}
          <span aria-hidden="true" className="text-muted-foreground">
            ·
          </span>
          {runner.last_seen_at ? (
            <RelativeTime
              value={runner.last_seen_at}
              locale={locale}
              className="font-mono text-muted-foreground tabular"
            >
              {(time) => t("servers.lastSeen", { time })}
            </RelativeTime>
          ) : (
            <span className="text-muted-foreground">{t("servers.neverSeen")}</span>
          )}
        </p>
      </div>
      {actions ? (
        <div className="flex shrink-0 flex-wrap items-start justify-end gap-1.5">{actions}</div>
      ) : null}
    </li>
  );
}
