import { useEffect, useId, useRef, useState } from "react";
import {
  usePushKey,
  useSubscribePush,
  useTestPush,
  useUnsubscribePush,
  useUpdateMe,
} from "@/entities/account";
import { isApiError } from "@/shared/api/client";
import type { Me } from "@/shared/api/types.gen";
import { type MessageKey, useT } from "@/shared/i18n";
import { ChoiceGroup } from "@/shared/ui/molecules/choice-group";
import { PageSection as Section } from "@/shared/ui/molecules/page-section";
import { Button } from "@/shared/ui/primitives/button";
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
} from "../lib/subscription";

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
export function NotificationsSection({ me }: { me: Me }) {
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
