import { zodResolver } from "@hookform/resolvers/zod";
import { createFileRoute } from "@tanstack/react-router";
import { MonitorIcon, MoonIcon, SunIcon } from "lucide-react";
import { type ReactNode, useId, useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { FormAlert, TextField } from "@/components/form-fields";
import { LOCALE_NAMES, useChooseLocale } from "@/components/language-switch";
import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Form } from "@/components/ui/form";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Skeleton } from "@/components/ui/skeleton";
import { isApiError } from "@/lib/api/client";
import {
  useChangePassword,
  useMe,
  useRevokeSession,
  useSessions,
  useUpdateMe,
} from "@/lib/api/queries";
import { type Me, PasswordMinLength, type Session } from "@/lib/api/types.gen";
import { applyFieldErrors } from "@/lib/forms";
import { LOCALES, type MessageKey, type Translate, useI18n, useT } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";
import {
  type ChangePasswordForm,
  changePasswordSchema,
  type ProfileForm,
  profileSchema,
} from "@/lib/schemas";
import type { ThemePreference } from "@/lib/theme";
import { formatAbsolute, formatRelative } from "@/lib/time";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/_app/settings")({
  component: SettingsPage,
});

function SettingsPage() {
  const t = useT();
  const { me: initialMe } = Route.useRouteContext();
  const me = useMe().data ?? initialMe;
  return (
    <>
      <PageHeader title={t("page.settingsTitle")} description={t("page.settingsBody")} />
      <div className="max-w-2xl divide-y px-4 md:px-8">
        <ProfileSection me={me} />
        <AppearanceSection />
        <PasswordSection />
        <SessionsSection />
      </div>
    </>
  );
}

function Section({
  id,
  title,
  description,
  children,
}: {
  id: string;
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <section aria-labelledby={id} className="grid gap-4 py-7 md:grid-cols-[12rem_1fr] md:gap-8">
      <div className="space-y-1">
        <h2 id={id} className="font-semibold tracking-[-0.01em]">
          {title}
        </h2>
        {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
      </div>
      <div className="min-w-0">{children}</div>
    </section>
  );
}

function formError(t: Translate, err: unknown) {
  return isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic");
}

function ProfileSection({ me }: { me: Me }) {
  const t = useT();
  const id = useId();
  const update = useUpdateMe();
  const [error, setError] = useState<string | null>(null);
  const form = useForm<ProfileForm>({
    resolver: zodResolver(profileSchema),
    defaultValues: { display_name: me.display_name },
  });
  const onSubmit = form.handleSubmit((values) => {
    setError(null);
    update.mutate(profileSchema.parse(values), {
      onSuccess: (next) => {
        form.reset({ display_name: next.display_name });
        toast.success(t("account.profileSaved"));
      },
      onError: (err) => {
        if (applyFieldErrors(err, form.setError, ["display_name"])) return;
        setError(formError(t, err));
      },
    });
  });

  return (
    <Section id={`${id}-profile`} title={t("account.profile")}>
      <Form {...form}>
        <form
          aria-labelledby={`${id}-profile`}
          onSubmit={onSubmit}
          noValidate
          className="space-y-4"
        >
          <FormAlert>{error}</FormAlert>
          <div className="space-y-1">
            <p className="text-sm font-medium">{t("auth.username")}</p>
            <p className="font-mono text-sm text-muted-foreground">@{me.username}</p>
          </div>
          <TextField
            control={form.control}
            name="display_name"
            label={t("auth.displayName")}
            description={t("auth.displayNameHint")}
            optional
            autoComplete="name"
          />
          <Button type="submit" variant="outline" disabled={update.isPending}>
            {t("account.saveProfile")}
          </Button>
        </form>
      </Form>
    </Section>
  );
}

const THEMES: { value: ThemePreference; icon: typeof SunIcon; label: MessageKey }[] = [
  { value: "system", icon: MonitorIcon, label: "theme.system" },
  { value: "light", icon: SunIcon, label: "theme.light" },
  { value: "dark", icon: MoonIcon, label: "theme.dark" },
];

function AppearanceSection() {
  const { t, locale } = useI18n();
  const id = useId();
  const theme = useLiveStore((s) => s.theme);
  const setTheme = useLiveStore((s) => s.setTheme);
  const chooseLocale = useChooseLocale(true);

  return (
    <Section
      id={`${id}-look`}
      title={t("account.appearance")}
      description={t("account.appearanceBody")}
    >
      <div className="space-y-6">
        <fieldset>
          <legend className="mb-2 text-sm font-medium">{t("theme.label")}</legend>
          <RadioGroup
            value={theme}
            onValueChange={(v) => setTheme(v as ThemePreference)}
            className="grid grid-cols-3 gap-2"
          >
            {THEMES.map(({ value, icon: Icon, label }) => (
              <Choice
                key={value}
                id={`${id}-theme-${value}`}
                value={value}
                checked={theme === value}
              >
                <Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                {t(label)}
              </Choice>
            ))}
          </RadioGroup>
        </fieldset>
        <fieldset>
          <legend className="mb-2 text-sm font-medium">{t("locale.label")}</legend>
          <RadioGroup
            value={locale}
            onValueChange={chooseLocale}
            className="grid gap-2 sm:grid-cols-2"
          >
            {LOCALES.map((l) => (
              <Choice key={l} id={`${id}-locale-${l}`} value={l} checked={locale === l} lang={l}>
                {LOCALE_NAMES[l]}
              </Choice>
            ))}
          </RadioGroup>
        </fieldset>
      </div>
    </Section>
  );
}

function Choice({
  id,
  value,
  checked,
  lang,
  children,
}: {
  id: string;
  value: string;
  checked: boolean;
  lang?: string;
  children: ReactNode;
}) {
  return (
    <label
      htmlFor={id}
      lang={lang}
      className={cn(
        "flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm transition-colors hover:bg-accent/60",
        checked && "border-tape-strong bg-tape/35 font-medium dark:bg-tape/12",
      )}
    >
      <RadioGroupItem id={id} value={value} />
      {children}
    </label>
  );
}

function PasswordSection() {
  const t = useT();
  const id = useId();
  const change = useChangePassword();
  const [error, setError] = useState<string | null>(null);
  const form = useForm<ChangePasswordForm>({
    resolver: zodResolver(changePasswordSchema),
    defaultValues: { current_password: "", new_password: "", repeat_password: "" },
  });
  const onSubmit = form.handleSubmit(({ current_password, new_password }) => {
    setError(null);
    change.mutate(
      { current_password, new_password },
      {
        onSuccess: () => {
          form.reset();
          toast.success(t("account.passwordChanged"));
        },
        onError: (err) => {
          if (applyFieldErrors(err, form.setError, ["current_password", "new_password"])) return;
          setError(formError(t, err));
        },
      },
    );
  });

  return (
    <Section
      id={`${id}-password`}
      title={t("auth.password")}
      description={t("account.passwordBody")}
    >
      <Form {...form}>
        <form
          aria-labelledby={`${id}-password`}
          onSubmit={onSubmit}
          noValidate
          className="space-y-4"
        >
          <FormAlert>{error}</FormAlert>
          <TextField
            control={form.control}
            name="current_password"
            type="password"
            label={t("account.currentPassword")}
            autoComplete="current-password"
          />
          <TextField
            control={form.control}
            name="new_password"
            type="password"
            label={t("account.newPassword")}
            description={t("auth.passwordHint", { min: PasswordMinLength })}
            autoComplete="new-password"
          />
          <TextField
            control={form.control}
            name="repeat_password"
            type="password"
            label={t("account.repeatPassword")}
            autoComplete="new-password"
          />
          <Button type="submit" variant="outline" disabled={change.isPending}>
            {t("account.changePassword")}
          </Button>
        </form>
      </Form>
    </Section>
  );
}

const BROWSERS: [RegExp, string][] = [
  [/Edg\//, "Edge"],
  [/Firefox\//, "Firefox"],
  [/Chrome\//, "Chrome"],
  [/Safari\//, "Safari"],
];
const SYSTEMS: [RegExp, string][] = [
  [/iPhone/, "iPhone"],
  [/iPad/, "iPad"],
  [/Android/, "Android"],
  [/Mac OS X|Macintosh/, "macOS"],
  [/Windows/, "Windows"],
  [/Linux|X11/, "Linux"],
];

/** "Chrome on Linux" from a user agent; good enough to recognise a device. */
export function describeDevice(t: Translate, userAgent: string): string {
  const browser = BROWSERS.find(([re]) => re.test(userAgent))?.[1];
  const os = SYSTEMS.find(([re]) => re.test(userAgent))?.[1];
  if (browser && os) return t("account.device", { browser, os });
  return browser ?? os ?? t("account.unknownDevice");
}

function SessionsSection() {
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
                      <span className="shrink-0 rounded-[3px] bg-tape px-1.5 py-px text-[0.7rem] font-semibold text-tape-foreground dark:bg-tape/20 dark:text-tape">
                        {t("account.thisDevice")}
                      </span>
                    ) : null}
                  </p>
                  <p className="flex flex-wrap gap-x-3 text-xs text-muted-foreground">
                    <span className="font-mono">{s.ip}</span>
                    <span title={formatAbsolute(s.last_seen_at, locale)}>
                      {t("account.lastActive", { time: formatRelative(s.last_seen_at, locale) })}
                    </span>
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
