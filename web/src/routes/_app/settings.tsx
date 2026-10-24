import { zodResolver } from "@hookform/resolvers/zod";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { MonitorIcon, MoonIcon, SunIcon } from "lucide-react";
import { type ReactNode, useId, useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { FormAlert, TextField } from "@/components/form-fields";
import { LOCALE_NAMES, useChooseLocale } from "@/components/language-switch";
import { NativeSelect } from "@/components/native-select";
import { PageHeader } from "@/components/page-header";
import { ProfileEditor } from "@/components/profile-editor";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Form } from "@/components/ui/form";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Skeleton } from "@/components/ui/skeleton";
import { isApiError } from "@/lib/api/client";
import {
  useChangePassword,
  useDeleteAccount,
  useIntelligence,
  useMe,
  useMyOrgs,
  useRevokeSession,
  useSessions,
  useSetDefaultOrg,
} from "@/lib/api/queries";
import {
  ErrorCodeLastInstanceAdmin,
  type Me,
  PasswordMinLength,
  type Session,
} from "@/lib/api/types.gen";
import { applyFieldErrors } from "@/lib/forms";
import { LOCALES, type MessageKey, type Translate, useI18n, useT } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";
import { defaultOrgIdOf } from "@/lib/orgs";
import {
  type ChangePasswordForm,
  changePasswordSchema,
  type DeleteAccountForm,
  deleteAccountSchema,
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
        <DefaultOrgSection />
        <PasswordSection />
        <SessionsSection />
        <DeleteAccountSection />
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

/**
 * The user's own profile (PROF-03), with the same editor as every intelligence: the display name,
 * the @handle, the bio and the photo. The answer patches the account everywhere it shows, the
 * sidebar header included.
 */
function ProfileSection({ me }: { me: Me }) {
  const t = useT();
  const id = useId();
  const profile = useIntelligence(me.intelligence.id);
  return (
    <Section
      id={`${id}-profile`}
      title={t("account.profile")}
      description={t("account.profileBody")}
    >
      {profile.data ? (
        <ProfileEditor profile={profile.data} />
      ) : profile.isError ? (
        <div className="space-y-2">
          <p role="alert" className="text-sm text-destructive">
            {t("profile.loadError")}
          </p>
          <Button variant="outline" size="sm" onClick={() => profile.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : (
        <div className="space-y-3" aria-hidden="true">
          <Skeleton className="size-24 rounded-full" />
          <Skeleton className="h-9 w-full max-w-sm" />
          <Skeleton className="h-9 w-full max-w-sm" />
        </div>
      )}
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
        checked && "border-tape-strong bg-tape/40 font-medium",
      )}
    >
      <RadioGroupItem id={id} value={value} />
      {children}
    </label>
  );
}

/**
 * The organization new AIs join when they are started without --org (ORG-04): one of the
 * organizations the account belongs to, guest ones included. Only a member can be chosen; the
 * server says 403 for any other, and the select goes back to what it was.
 */
function DefaultOrgSection() {
  const t = useT();
  const id = useId();
  const me = useMe().data;
  const mine = useMyOrgs();
  const setDefault = useSetDefaultOrg();
  const [choice, setChoice] = useState<string | null>(null);
  const orgs = mine.data ?? [];

  if (mine.isLoading) {
    return (
      <Section id={`${id}-org`} title={t("account.defaultOrg")}>
        <Skeleton aria-hidden="true" className="h-9 w-full max-w-sm" />
      </Section>
    );
  }
  // Without organizations to choose from there is nothing to set (or they could not be read).
  if (orgs.length === 0) return null;

  // A default the account no longer belongs to counts for nothing: the select asks for a choice.
  const defaultId = defaultOrgIdOf(me);
  const current = orgs.some((o) => o.id === defaultId) ? (defaultId ?? "") : "";
  const value = choice ?? current;

  const choose = (orgId: string) => {
    if (orgId === value) return;
    setChoice(orgId);
    setDefault.mutate(orgId, {
      onSuccess: () =>
        toast.success(
          t("account.defaultOrgSaved", { org: orgs.find((o) => o.id === orgId)?.name ?? "" }),
        ),
      onError: (err) =>
        toast.error(
          isApiError(err) && err.status === 403
            ? t("account.defaultOrgForbidden")
            : t("account.defaultOrgFailed"),
        ),
      onSettled: () => setChoice(null),
    });
  };

  return (
    <Section
      id={`${id}-org`}
      title={t("account.defaultOrg")}
      description={t("account.defaultOrgBody")}
    >
      <NativeSelect
        aria-label={t("account.defaultOrgLabel")}
        value={value}
        disabled={setDefault.isPending}
        onChange={(e) => choose(e.target.value)}
        className="max-w-sm"
      >
        {value === "" ? (
          <option value="" disabled>
            {t("account.defaultOrgNone")}
          </option>
        ) : null}
        {orgs.map((o) => (
          <option key={o.id} value={o.id}>
            {o.name}
          </option>
        ))}
      </NativeSelect>
    </Section>
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
                      <span className="shrink-0 rounded-[3px] bg-tape px-1.5 py-px text-[0.7rem] font-semibold text-tape-foreground">
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

/**
 * Deleting one's own account (POS-06): the password proves who asks, and the dialog says what
 * stays. The AIs, groups and connections belong to their organizations, so they outlive the person.
 */
function DeleteAccountSection() {
  const t = useT();
  const id = useId();
  const [open, setOpen] = useState(false);
  return (
    <Section id={`${id}-delete`} title={t("account.delete")} description={t("account.deleteBody")}>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogTrigger asChild>
          <Button variant="outline" className="text-destructive hover:text-destructive">
            {t("account.deleteOpen")}
          </Button>
        </DialogTrigger>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("account.deleteTitle")}</DialogTitle>
            <DialogDescription>{t("account.deleteDialogBody")}</DialogDescription>
          </DialogHeader>
          {/* Mounted only while open: every opening starts with an empty password. */}
          {open ? <DeleteAccountBody onCancel={() => setOpen(false)} /> : null}
        </DialogContent>
      </Dialog>
    </Section>
  );
}

function DeleteAccountBody({ onCancel }: { onCancel: () => void }) {
  const t = useT();
  const navigate = useNavigate();
  const remove = useDeleteAccount();
  const [error, setError] = useState<string | null>(null);
  const form = useForm<DeleteAccountForm>({
    resolver: zodResolver(deleteAccountSchema),
    defaultValues: { password: "" },
  });
  const onSubmit = form.handleSubmit(({ password }) => {
    if (remove.isPending) return;
    setError(null);
    remove.mutate(
      { password },
      {
        onSuccess: () => {
          toast(t("account.deleted"));
          void navigate({ to: "/login", search: { redirect: undefined } });
        },
        onError: (err) => {
          if (applyFieldErrors(err, form.setError, ["password"])) return;
          // The last admin of the instance is needed to inherit the organizations (OUPG-02).
          if (isApiError(err) && err.code === ErrorCodeLastInstanceAdmin) {
            setError(t("account.deleteLastAdmin"));
            return;
          }
          setError(formError(t, err));
        },
      },
    );
  });

  return (
    <Form {...form}>
      <form onSubmit={onSubmit} noValidate className="space-y-4">
        <FormAlert>{error}</FormAlert>
        <TextField
          control={form.control}
          name="password"
          type="password"
          label={t("auth.password")}
          autoComplete="current-password"
          autoFocus
        />
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onCancel}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" variant="destructive" disabled={remove.isPending}>
            {t("account.deleteConfirm")}
          </Button>
        </DialogFooter>
      </form>
    </Form>
  );
}
