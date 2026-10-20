import { createFileRoute, Link } from "@tanstack/react-router";
import {
  CircleCheckIcon,
  CircleSlashIcon,
  LaptopIcon,
  SearchXIcon,
  ShieldAlertIcon,
  TimerOffIcon,
} from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { EmptyState } from "@/components/empty-state";
import { FormAlert } from "@/components/form-fields";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { isApiError } from "@/lib/api/client";
import { useDecideDevice, useDeviceRequest } from "@/lib/api/queries";
import type { DeviceRequest } from "@/lib/api/types.gen";
import { useI18n, useT } from "@/lib/i18n";
import { formatAbsolute, formatRelative } from "@/lib/time";
import { cn } from "@/lib/utils";

/*
 * Approve a machine (WIZ-02, DEV-02): `partyline setup --browser` prints a link to this page.
 * The page shows what asks for access (machine name, address, age, code) so a forwarded link is
 * easy to spot, and approves or denies it. Approving creates an agent token owned by the viewer,
 * which the waiting CLI receives once. Logged-out visitors go through login and come back here.
 */

export const Route = createFileRoute("/_app/devices/$code")({
  component: DevicePage,
});

/** Seconds until iso, ticking every second while the request is pending. */
function useSecondsLeft(iso: string | undefined, ticking: boolean) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!iso || !ticking) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [iso, ticking]);
  if (!iso) return 0;
  return Math.max(0, Math.ceil((Date.parse(iso) - now) / 1000));
}

const clock = (seconds: number) =>
  `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;

function DevicePage() {
  const t = useT();
  const { code } = Route.useParams();
  const request = useDeviceRequest(code);

  if (request.isError) {
    const missing = isApiError(request.error) && request.error.status === 404;
    return (
      <EmptyState
        icon={missing ? SearchXIcon : ShieldAlertIcon}
        title={missing ? t("devices.notFoundTitle") : t("devices.loadFailed")}
        action={
          missing ? null : (
            <Button variant="outline" onClick={() => request.refetch()}>
              {t("common.retry")}
            </Button>
          )
        }
      >
        {missing ? t("devices.notFoundBody") : null}
      </EmptyState>
    );
  }
  if (!request.data) {
    return (
      <div className="mx-auto w-full max-w-lg px-4 py-10 md:py-16" aria-busy="true">
        <Skeleton className="h-7 w-56" />
        <Skeleton className="mt-3 h-4 w-72" />
        <Skeleton className="mt-8 h-56 w-full" />
      </div>
    );
  }
  return <Request request={request.data} code={code} />;
}

function Request({ request, code }: { request: DeviceRequest; code: string }) {
  const t = useT();
  const { locale } = useI18n();
  const decide = useDecideDevice(code);
  const [error, setError] = useState<string | null>(null);
  const [expiredNow, setExpiredNow] = useState(false);
  const pending = request.status === "pending";
  const secondsLeft = useSecondsLeft(request.expires_at, pending);
  // A pending request reads as expired once its time is up (the server says so too).
  const status = pending && (secondsLeft === 0 || expiredNow) ? "expired" : request.status;

  const run = (decision: "approve" | "deny") => {
    setError(null);
    decide.mutate(decision, {
      onError: (err) => {
        if (isApiError(err) && err.status === 410) setExpiredNow(true);
        else if (isApiError(err) && err.status === 409) setError(t("devices.alreadyDecided"));
        else if (isApiError(err) && err.status === 0) setError(t("error.network"));
        else setError(t("error.generic"));
      },
    });
  };

  if (status === "expired") {
    return (
      <Outcome icon={TimerOffIcon} title={t("devices.expiredTitle")}>
        <InlineCommand text={t("devices.expiredBody")} command="partyline setup --browser" />
      </Outcome>
    );
  }
  if (status === "denied") {
    return (
      <Outcome
        icon={CircleSlashIcon}
        title={t("devices.deniedTitle", { machine: request.machine })}
      >
        {t("devices.deniedBody")}
      </Outcome>
    );
  }
  if (status === "approved") {
    return (
      <Outcome
        icon={CircleCheckIcon}
        title={t("devices.approvedTitle", { machine: request.machine })}
        action={
          <Button asChild variant="outline">
            <Link to="/agents">{t("devices.toAgents")}</Link>
          </Button>
        }
      >
        {t("devices.approvedBody")}
      </Outcome>
    );
  }

  return (
    <div className="mx-auto w-full max-w-lg px-4 py-10 md:py-16">
      <h1 className="text-xl font-semibold tracking-[-0.01em] text-balance">
        {t("devices.title")}
      </h1>
      <p className="mt-2 text-sm text-pretty text-muted-foreground">{t("devices.body")}</p>

      <section
        aria-label={t("devices.request")}
        className="mt-7 overflow-hidden rounded-md border bg-card shadow-sm"
      >
        <div className="flex items-center gap-3 border-b px-4 py-3.5">
          <span
            aria-hidden="true"
            className="flex size-9 shrink-0 items-center justify-center rounded-[3px] bg-tape text-tape-foreground dark:bg-tape/20 dark:text-tape"
          >
            <LaptopIcon className="size-4.5" strokeWidth={1.75} />
          </span>
          <div className="min-w-0">
            <p className="label-caps text-muted-foreground">{t("devices.machine")}</p>
            <p className="truncate font-mono text-[0.95rem] font-medium" translate="no">
              {request.machine}
            </p>
          </div>
        </div>
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-6 gap-y-2.5 px-4 py-4 text-sm">
          <Field label={t("devices.code")}>
            <span className="font-mono text-base font-semibold tracking-[0.18em]" translate="no">
              {request.code}
            </span>
          </Field>
          <Field label={t("devices.ip")}>
            <span className="font-mono" translate="no">
              {request.ip}
            </span>
          </Field>
          <Field label={t("devices.requested")}>
            <time dateTime={request.created_at} title={formatAbsolute(request.created_at, locale)}>
              {formatRelative(request.created_at, locale)}
            </time>
          </Field>
          <Field label={t("devices.expires")}>
            <span
              className={cn("tabular font-mono", secondsLeft <= 60 && "text-destructive")}
              role="timer"
              aria-live="off"
            >
              {clock(secondsLeft)}
            </span>
          </Field>
        </dl>
        <div className="flex gap-2.5 border-t bg-warning/10 px-4 py-3 text-sm">
          <ShieldAlertIcon className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          <p className="text-pretty">{t("devices.warning")}</p>
        </div>
      </section>

      <p className="mt-4 text-xs text-pretty text-muted-foreground">{t("devices.tokenNote")}</p>

      {error ? (
        <div className="mt-4">
          <FormAlert>{error}</FormAlert>
        </div>
      ) : null}

      <div className="mt-6 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
        <Button
          type="button"
          variant="outline"
          disabled={decide.isPending}
          onClick={() => run("deny")}
        >
          {t("devices.deny")}
        </Button>
        <Button type="button" disabled={decide.isPending} onClick={() => run("approve")}>
          {decide.isPending && decide.variables === "approve"
            ? t("devices.approving")
            : t("devices.approve")}
        </Button>
      </div>
    </div>
  );
}

/** Renders `{cmd}` in a sentence as an unbreakable inline command. */
function InlineCommand({ text, command }: { text: string; command: string }) {
  const [before, after = ""] = text.split("{cmd}");
  return (
    <>
      {before}
      <code translate="no" className="font-mono whitespace-nowrap text-foreground">
        {command}
      </code>
      {after}
    </>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="pt-px text-muted-foreground">{label}</dt>
      <dd className="min-w-0 text-foreground">{children}</dd>
    </>
  );
}

function Outcome({
  icon,
  title,
  children,
  action,
}: {
  icon: typeof LaptopIcon;
  title: string;
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <EmptyState icon={icon} title={title} action={action}>
      {children}
    </EmptyState>
  );
}
