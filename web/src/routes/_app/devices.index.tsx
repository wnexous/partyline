import { createFileRoute, Link } from "@tanstack/react-router";
import {
  CircleCheckIcon,
  CircleSlashIcon,
  LaptopIcon,
  ShieldAlertIcon,
  TimerOffIcon,
} from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useId, useState } from "react";
import { EmptyState } from "@/shared/ui/molecules/empty-state";
import { FormAlert } from "@/shared/ui/molecules/form-fields";
import { Button } from "@/shared/ui/primitives/button";
import { Input } from "@/shared/ui/primitives/input";
import { Label } from "@/shared/ui/primitives/label";
import { useAutoFocus } from "@/shared/lib/device";
import { isApiError } from "@/shared/api/client";
import { useDeviceAction } from "@/lib/api/queries";
import type { DeviceRequest } from "@/shared/api/types.gen";
import { type Translate, useI18n, useT } from "@/shared/i18n";
import { formatAbsolute, formatRelative } from "@/shared/lib/time";
import { cn } from "@/shared/lib/utils";

/*
 * Approve a machine (WIZ-02, DEV-02, DEV-05): `partyline setup --browser` prints this page's
 * address and a code. The viewer types the code from their own terminal, so a link someone
 * forwards cannot approve anything; the page then shows what asks for access (machine name,
 * address, age) and approves or denies it. Approving creates an agent token owned by the
 * viewer, which the waiting CLI receives once, together with the viewer's username.
 * Logged-out visitors go through login and come back here.
 */

export const Route = createFileRoute("/_app/devices/")({
  component: DevicesPage,
});

/** The code as the server reads it: upper case, without spaces and hyphens. */
export function normalizeCode(code: string) {
  return code.toUpperCase().replace(/[\s-]/g, "");
}

/** Message for a failed code entry: unknown code, too many wrong codes, network. */
function codeError(err: unknown, t: Translate) {
  if (isApiError(err) && err.status === 404) return t("devices.codeNotFound");
  if (isApiError(err) && err.status === 429) return t("devices.tooManyCodes");
  if (isApiError(err) && err.status === 0) return t("error.network");
  return t("error.generic");
}

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

function DevicesPage() {
  const [found, setFound] = useState<{ request: DeviceRequest; code: string } | null>(null);
  if (!found) return <CodeEntry onFound={(request, code) => setFound({ request, code })} />;
  return (
    <Request
      request={found.request}
      code={found.code}
      onChange={(request) => setFound({ request, code: found.code })}
      onBack={() => setFound(null)}
    />
  );
}

/** First step: the code from the terminal. Nothing about the request shows before it. */
function CodeEntry({ onFound }: { onFound: (request: DeviceRequest, code: string) => void }) {
  const t = useT();
  const id = useId();
  const autoFocus = useAutoFocus();
  const lookup = useDeviceAction();
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const typed = normalizeCode(code);
    if (typed.length !== 8) {
      setError(t("devices.codeShape"));
      return;
    }
    setError(null);
    lookup.mutate(
      { action: "lookup", code: typed },
      {
        onSuccess: (request) => onFound(request, typed),
        onError: (err) => setError(codeError(err, t)),
      },
    );
  };

  return (
    <div className="mx-auto w-full max-w-lg px-4 py-10 md:py-16">
      <h1 className="text-xl font-semibold tracking-[-0.01em] text-balance">
        {t("devices.title")}
      </h1>
      <p className="mt-2 text-sm text-pretty text-muted-foreground">
        <InlineCommand text={t("devices.enterBody")} command="partyline setup --browser" />
      </p>
      <form onSubmit={submit} className="mt-7 space-y-4" noValidate>
        <div className="space-y-2">
          <Label htmlFor={id}>{t("devices.codeLabel")}</Label>
          <Input
            id={id}
            value={code}
            onChange={(e) => setCode(e.target.value)}
            placeholder="ABCD-EFGH"
            autoComplete="off"
            autoCapitalize="characters"
            autoCorrect="off"
            spellCheck={false}
            autoFocus={autoFocus}
            maxLength={16}
            aria-invalid={error ? true : undefined}
            className="h-11 font-mono text-lg tracking-[0.18em] uppercase"
            translate="no"
          />
        </div>
        <FormAlert>{error}</FormAlert>
        <div className="flex justify-end">
          <Button type="submit" disabled={lookup.isPending}>
            {lookup.isPending ? t("devices.checking") : t("devices.continue")}
          </Button>
        </div>
      </form>
      <div className="mt-8 flex gap-2.5 rounded-md border border-tape-strong/40 bg-muted px-4 py-3 text-sm">
        <ShieldAlertIcon className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
        <p className="text-pretty">{t("devices.warning")}</p>
      </div>
    </div>
  );
}

function Request({
  request,
  code,
  onChange,
  onBack,
}: {
  request: DeviceRequest;
  code: string;
  onChange: (request: DeviceRequest) => void;
  onBack: () => void;
}) {
  const t = useT();
  const { locale } = useI18n();
  const decide = useDeviceAction();
  const [error, setError] = useState<string | null>(null);
  const [expiredNow, setExpiredNow] = useState(false);
  const pending = request.status === "pending";
  const secondsLeft = useSecondsLeft(request.expires_at, pending);
  // A pending request reads as expired once its time is up (the server says so too).
  const status = pending && (secondsLeft === 0 || expiredNow) ? "expired" : request.status;

  const run = (action: "approve" | "deny") => {
    setError(null);
    decide.mutate(
      { action, code },
      {
        onSuccess: onChange,
        onError: (err) => {
          if (isApiError(err) && err.status === 410) setExpiredNow(true);
          else if (isApiError(err) && err.status === 409) {
            // Decided elsewhere: show what was decided.
            setError(t("devices.alreadyDecided"));
            decide.mutate({ action: "lookup", code }, { onSuccess: onChange });
          } else setError(codeError(err, t));
        },
      },
    );
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
            <Link to="/contacts">{t("devices.toContacts")}</Link>
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
            className="flex size-9 shrink-0 items-center justify-center rounded-[3px] bg-tape text-tape-foreground"
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
              className={cn(
                "tabular font-mono",
                secondsLeft <= 60 && "rounded-[3px] bg-foreground px-1.5 text-background",
              )}
              role="timer"
              aria-live="off"
            >
              {clock(secondsLeft)}
            </span>
          </Field>
        </dl>
        <div className="flex gap-2.5 border-t bg-muted px-4 py-3 text-sm">
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
          variant="ghost"
          className="sm:mr-auto"
          disabled={decide.isPending}
          onClick={onBack}
        >
          {t("devices.otherCode")}
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={decide.isPending}
          onClick={() => run("deny")}
        >
          {t("devices.deny")}
        </Button>
        <Button type="button" disabled={decide.isPending} onClick={() => run("approve")}>
          {decide.isPending && decide.variables?.action === "approve"
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
