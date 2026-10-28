import { zodResolver } from "@hookform/resolvers/zod";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { MonitorIcon, MoonIcon, SunIcon } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { useForm } from "react-hook-form";
import { toast } from "sonner";
import { LOCALE_NAMES, useChooseLocale } from "@/components/language-switch";
import { ProfileEditor } from "@/components/profile-editor";
import {
  useChangePassword,
  useDeleteAccount,
  useIntelligence,
  useMe,
  useMyOrgs,
  usePushKey,
  useRevokeSession,
  useSessions,
  useSetDefaultOrg,
  useSubscribePush,
  useTestPush,
  useUnsubscribePush,
  useUpdateMe,
} from "@/lib/api/queries";
import { useLiveStore } from "@/lib/live/store";
import { defaultOrgIdOf } from "@/lib/orgs";
import {
  activeRegistration,
  applicationServerKey,
  currentSubscription,
  permissionState,
  pushSupported,
  requestPermission,
  subscribe,
  subscriptionJSON,
  unsubscribe,
} from "@/lib/push/subscription";
import {
  type ChangePasswordForm,
  changePasswordSchema,
  type DeleteAccountForm,
  deleteAccountSchema,
} from "@/lib/schemas";
import type { ThemePreference } from "@/lib/theme";
import { isApiError } from "@/shared/api/client";
import {
  ErrorCodeLastInstanceAdmin,
  type Me,
  PasswordMinLength,
  type Session,
} from "@/shared/api/types.gen";
import { LOCALES, type MessageKey, type Translate, useI18n, useT } from "@/shared/i18n";
import { applyFieldErrors } from "@/shared/lib/forms";
import { NativeSelect } from "@/shared/ui/atoms/native-select";
import { RelativeTime } from "@/shared/ui/atoms/relative-time";
import { ChoiceGroup } from "@/shared/ui/molecules/choice-group";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/shared/ui/molecules/dialog";
import { FormAlert, TextField } from "@/shared/ui/molecules/form-fields";
import { PageHeader } from "@/shared/ui/molecules/page-header";
import { PageSection as Section } from "@/shared/ui/molecules/page-section";
import { Button } from "@/shared/ui/primitives/button";
import { Form } from "@/shared/ui/primitives/form";
import { Skeleton } from "@/shared/ui/primitives/skeleton";

export const Route = createFileRoute("/_app/account/")({
  component: SettingsPage,
});

function SettingsPage() {
  const t = useT();
  const { me: initialMe } = Route.useRouteContext();
  const me = useMe().data ?? initialMe;
  return (
    <>
      <PageHeader title={t("page.accountTitle")} description={t("page.accountBody")} />
      <div className="max-w-2xl divide-y px-4 md:px-8">
        <ProfileSection me={me} />
        <AppearanceSection />
        <NotificationsSection me={me} />
        <DefaultOrgSection />
        <PasswordSection />
        <SessionsSection />
        <DeleteAccountSection />
      </div>
    </>
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
        <ChoiceGroup
          legend={t("theme.label")}
          idBase={`${id}-theme`}
          value={theme}
          onValueChange={(v) => setTheme(v as ThemePreference)}
          className="sm:grid-cols-3"
          emphasizeChecked
          options={THEMES.map(({ value, icon: Icon, label }) => ({
            value,
            icon: <Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />,
            label: t(label),
          }))}
        />
        <ChoiceGroup
          legend={t("locale.label")}
          idBase={`${id}-locale`}
          value={locale}
          onValueChange={chooseLocale}
          className="sm:grid-cols-2"
          emphasizeChecked
          options={LOCALES.map((l) => ({ value: l, label: LOCALE_NAMES[l], lang: l }))}
        />
      </div>
    </Section>
  );
}

type PushGroup = "all" | "mentions" | "off";
type DeviceState = "unsupported" | "blocked" | "off" | "on";

const PUSH_GROUPS: { value: PushGroup; label: MessageKey }[] = [
  { value: "all", label: "push.group.all" },
  { value: "mentions", label: "push.group.mentions" },
  { value: "off", label: "push.group.off" },
];

/** The account's group setting; the server only stores all, mentions or off (PUSH-06). */
function pushGroupOf(me: Me): PushGroup {
  return PUSH_GROUPS.find((choice) => choice.value === me.push_groups)?.value ?? "mentions";
}

/**
 * Notifications on this device (PUSH-18, PUSH-21, PUSH-22): the browser's permission and
 * subscription, the test notification, and which group messages notify this account. Permission
 * is asked for only when the user presses Enable; mounting the page never prompts. A browser
 * that cannot push keeps the rest of Settings usable and says why.
 */
function NotificationsSection({ me }: { me: Me }) {
  const t = useT();
  const id = useId();
  const supported = pushSupported();
  // The browser's subscription is not React state: it lives in the browser, which answers async.
  const held = useRef<PushSubscription | null>(null);
  const [device, setDevice] = useState<DeviceState>(() =>
    supported ? (permissionState() === "denied" ? "blocked" : "off") : "unsupported",
  );
  const [busy, setBusy] = useState<"enable" | "disable" | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Which step of enabling failed and why, shown only under the enable sentence; the other
  // failures set `error` alone.
  const [errorDetail, setErrorDetail] = useState<string | null>(null);
  const [groupError, setGroupError] = useState<string | null>(null);
  const key = usePushKey();
  const store = useSubscribePush();
  const remove = useUnsubscribePush();
  const test = useTestPush();
  const updateMe = useUpdateMe();

  // Whether this browser already holds a subscription; a granted permission never prompts again.
  useEffect(() => {
    if (!supported || permissionState() !== "granted") return;
    let live = true;
    void currentSubscription().then(
      (sub) => {
        if (!live) return;
        held.current = sub;
        setDevice(sub ? "on" : "off");
      },
      () => {
        if (live) setDevice("off");
      },
    );
    return () => {
      live = false;
    };
  }, [supported]);

  const enable = async () => {
    if (busy) return;
    setBusy("enable");
    setNotice(null);
    setError(null);
    setErrorDetail(null);
    // Enabling has four steps; remembering which one failed is the only way a phone can tell a
    // worker that never got ready apart from a push service that refused (PUSH-18).
    type Step = "key" | "worker" | "subscribe" | "server";
    const stepNames: Record<Step, MessageKey> = {
      key: "push.step.key",
      worker: "push.step.worker",
      subscribe: "push.step.subscribe",
      server: "push.step.server",
    };
    let step: Step = "key";
    // Only the error's name and message: never the endpoint or the keys of a subscription. A
    // DOMException is not an Error, so its name and message are read off the value.
    const thrown = (err: unknown): string => {
      const { name, message } = (err ?? {}) as { name?: unknown; message?: unknown };
      const label = typeof name === "string" && name !== "" ? name : "Error";
      const text = typeof message === "string" ? message : String(err);
      return `${label}: ${text}`.slice(0, 160);
    };
    const reason = (err: unknown): string => {
      if (step === "worker") return t("push.step.workerDetail");
      if (step === "key" && isApiError(err)) return String(err.status);
      if (step === "server" && isApiError(err)) return `${err.status} ${err.code}`;
      return thrown(err);
    };
    try {
      const answer = await requestPermission();
      if (answer !== "granted") {
        setDevice(answer === "denied" ? "blocked" : "off");
        return;
      }
      // `refetch` resolves with the failure instead of throwing it, so the status is read here.
      const fetched = await key.refetch();
      if (fetched.error) throw fetched.error;
      const publicKey = fetched.data?.public_key;
      if (!publicKey) throw new Error("push key unavailable");
      step = "worker";
      // Waiting for the worker here is what tells a registration that never got ready apart from
      // a subscription the push service refused.
      await activeRegistration();
      step = "subscribe";
      const created = await subscribe(applicationServerKey(publicKey));
      step = "server";
      try {
        await store.mutateAsync(subscriptionJSON(created));
      } catch (err) {
        // The browser and the server must agree: drop the subscription the server refused.
        await unsubscribe();
        throw err;
      }
      held.current = created;
      setDevice("on");
    } catch (err) {
      const line = t("push.step.line", { step: t(stepNames[step]), detail: reason(err) });
      setError(t("push.enableFailed"));
      setErrorDetail(line);
      // Remote debugging on a phone is the only place this is seen; no subscription data is logged.
      console.warn(line);
    } finally {
      setBusy(null);
    }
  };

  const disable = async () => {
    if (busy) return;
    setBusy("disable");
    setNotice(null);
    setError(null);
    try {
      const sub = held.current;
      if (!sub) {
        setDevice("off");
        return;
      }
      const { endpoint } = subscriptionJSON(sub);
      await unsubscribe();
      held.current = null;
      try {
        await remove.mutateAsync(endpoint);
      } catch (err) {
        // A row another device already removed is the state this button wants (PUSH-05).
        if (!(isApiError(err) && err.status === 404)) throw err;
      }
      setDevice("off");
    } catch {
      setError(t("push.disableFailed"));
    } finally {
      setBusy(null);
    }
  };

  const sendTest = () => {
    setNotice(null);
    setError(null);
    test.mutate(undefined, {
      onSuccess: () => setNotice(t("push.testQueued")),
      onError: (err) => {
        if (isApiError(err) && err.status === 409) setError(t("push.testNoSubscription"));
        else if (isApiError(err) && err.status === 429) setError(t("push.testRateLimited"));
        else setError(t("push.testFailed"));
      },
    });
  };

  const group = pushGroupOf(me);
  const chooseGroup = (value: string) => {
    if (value === group) return;
    setGroupError(null);
    updateMe.mutate(
      { push_groups: value },
      { onError: () => setGroupError(t("push.groupFailed")) },
    );
  };

  return (
    <Section id={`${id}-notifications`} title={t("push.title")} description={t("push.body")}>
      <div className="space-y-6">
        <div className="space-y-3">
          <p role="status" className="text-sm text-muted-foreground">
            {device === "unsupported"
              ? t("push.state.unsupported")
              : device === "blocked"
                ? t("push.state.blocked")
                : device === "on"
                  ? t("push.state.on")
                  : t("push.state.off")}
          </p>
          {supported ? (
            <div className="flex flex-wrap gap-2">
              {device === "on" ? null : (
                <Button
                  type="button"
                  disabled={device === "blocked" || busy !== null}
                  onClick={() => void enable()}
                >
                  {t("push.enable")}
                </Button>
              )}
              {/* A test can only fail while this device holds no subscription: say so by disabling. */}
              <Button
                type="button"
                variant="outline"
                disabled={busy !== null || device !== "on"}
                onClick={sendTest}
              >
                {t("push.test")}
              </Button>
              {device === "on" ? (
                <Button
                  type="button"
                  variant="outline"
                  disabled={busy !== null}
                  onClick={() => void disable()}
                >
                  {t("push.disable")}
                </Button>
              ) : null}
            </div>
          ) : null}
          {notice ? (
            <p role="status" className="text-sm text-muted-foreground">
              {notice}
            </p>
          ) : null}
          {error ? (
            <div role="alert" className="text-sm text-destructive">
              <p>{error}</p>
              {/* A step detail belongs to the enable sentence; other failures set `error` only. */}
              {errorDetail && error === t("push.enableFailed") ? (
                <p className="mt-1 text-muted-foreground">{errorDetail}</p>
              ) : null}
            </div>
          ) : null}
        </div>
        <ChoiceGroup
          legend={t("push.group.label")}
          idBase={`${id}-push`}
          value={group}
          onValueChange={chooseGroup}
          disabled={updateMe.isPending}
          emphasizeChecked
          options={PUSH_GROUPS.map(({ value, label }) => ({ value, label: t(label) }))}
        >
          {groupError ? (
            <p role="alert" className="mt-2 text-sm text-destructive">
              {groupError}
            </p>
          ) : null}
        </ChoiceGroup>
      </div>
    </Section>
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
