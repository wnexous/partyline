import { createFileRoute, Link } from "@tanstack/react-router";
import { CircleCheckIcon, FingerprintIcon, LinkIcon, TimerOffIcon, XIcon } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { toast } from "sonner";
import {
  useDeleteIdentity,
  useIdentities,
  useLinkedIdentityStore,
  useStartPairing,
} from "@/entities/account";
import { useConnectors } from "@/entities/connector";
import { platformName } from "@/entities/message";
import { useMyOrgs } from "@/entities/organization";
import type { Connector, Identity, PairingCode } from "@/shared/api/types.gen";
import { useI18n, useT } from "@/shared/i18n";
import { formatAbsolute } from "@/shared/lib/time";
import { cn } from "@/shared/lib/utils";
import { ChoiceGroup } from "@/shared/ui/molecules/choice-group";
import { ConfirmDialog } from "@/shared/ui/molecules/confirm-dialog";
import { CodeBlock } from "@/shared/ui/molecules/copy-field";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/shared/ui/molecules/dialog";
import { EmptyState } from "@/shared/ui/molecules/empty-state";
import { PageHeader } from "@/shared/ui/molecules/page-header";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";

export const Route = createFileRoute("/_app/identities")({
  component: IdentitiesPage,
});

/*
 * Verified chat identities (UI-18, IDN-01..04). Linking proves control of a chat account: the
 * user picks the connector that reads the chat (codes are bound to one connector), gets a
 * 6-character code and sends `!link CODE` (or `/link CODE`) from that account within 10 minutes.
 * The identity.linked live event flips the dialog to success.
 */

function IdentitiesPage() {
  const t = useT();
  const identities = useIdentities();
  const [linking, setLinking] = useState(false);
  const [removing, setRemoving] = useState<Identity | null>(null);
  const list = identities.data ?? [];

  const link = (
    <Button onClick={() => setLinking(true)}>
      <LinkIcon aria-hidden="true" />
      {t("identities.link")}
    </Button>
  );

  return (
    <>
      <PageHeader
        title={t("page.identitiesTitle")}
        description={t("page.identitiesBody")}
        actions={list.length > 0 ? link : null}
      />
      {identities.isPending ? (
        <div className="space-y-3 px-4 py-6 md:px-8" aria-hidden="true">
          <Skeleton className="h-4 w-48" />
          <Skeleton className="h-4 w-64 max-w-full" />
        </div>
      ) : identities.isError ? (
        <div className="px-4 py-10 text-sm md:px-8" role="alert">
          <p className="text-destructive">{t("identities.loadError")}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => identities.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : list.length === 0 ? (
        <EmptyState icon={FingerprintIcon} title={t("identities.emptyTitle")} action={link}>
          <p>{t("identities.emptyBody")}</p>
        </EmptyState>
      ) : (
        <ul aria-label={t("identities.listLabel")} className="divide-y border-b">
          {list.map((identity) => (
            <IdentityRow
              key={identity.id}
              identity={identity}
              onRemove={() => setRemoving(identity)}
            />
          ))}
        </ul>
      )}
      <PairingDialog open={linking} onOpenChange={setLinking} />
      <RemoveIdentityDialog identity={removing} onClose={() => setRemoving(null)} />
    </>
  );
}

function IdentityRow({ identity, onRemove }: { identity: Identity; onRemove: () => void }) {
  const { t, locale } = useI18n();
  return (
    <li aria-label={identity.display} className="flex items-center gap-4 px-4 py-3.5 md:px-8">
      <FingerprintIcon className="size-4 shrink-0 text-foreground" aria-hidden="true" />
      <div className="min-w-0 flex-1">
        <p className="flex min-w-0 items-center gap-2">
          <span className="truncate font-semibold tracking-[-0.01em]">{identity.display}</span>
          <span className="shrink-0 rounded-[3px] border px-1.5 py-px text-[0.7rem] text-muted-foreground">
            {platformName(identity.connector_kind)}
          </span>
        </p>
        <p className="mt-0.5 flex min-w-0 flex-wrap gap-x-3 text-xs text-muted-foreground">
          <span className="truncate font-mono">{identity.external_id}</span>
          <span>
            {t("identities.linkedOn", { date: formatAbsolute(identity.created_at, locale) })}
          </span>
        </p>
      </div>
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label={t("identities.remove", { name: identity.display })}
        className="text-muted-foreground hover:text-destructive"
        onClick={onRemove}
      >
        <XIcon aria-hidden="true" />
      </Button>
    </li>
  );
}

function RemoveIdentityDialog({
  identity,
  onClose,
}: {
  identity: Identity | null;
  onClose: () => void;
}) {
  const t = useT();
  const remove = useDeleteIdentity();
  return (
    <ConfirmDialog
      open={identity !== null}
      onClose={onClose}
      title={t("identities.removeTitle", { name: identity?.display ?? "" })}
      body={t("identities.removeBody")}
      confirmLabel={t("identities.removeConfirm")}
      pending={remove.isPending}
      onConfirm={() => {
        if (!identity) return;
        remove.mutate(identity.id, {
          onSuccess: () => onClose(),
          onError: () => toast.error(t("identities.removeFailed")),
        });
      }}
    />
  );
}

/** Seconds left until `iso`, ticking every second. */
function useSecondsLeft(iso: string | undefined) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!iso) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [iso]);
  if (!iso) return 0;
  return Math.max(0, Math.ceil((Date.parse(iso) - now) / 1000));
}

const clock = (seconds: number) =>
  `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;

/** Controlled by the page so it survives the list switching from empty to filled. */
function PairingDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const t = useT();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("identities.link")}</DialogTitle>
          <DialogDescription>{t("pairing.body")}</DialogDescription>
        </DialogHeader>
        {open ? <PairingBody onDone={() => onOpenChange(false)} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function PairingBody({ onDone }: { onDone: () => void }) {
  const t = useT();
  const connectors = useConnectors();
  const [picked, setPicked] = useState<Connector | null>(null);
  // Only an identity linked after the dialog opened counts as this pairing's result.
  const [baseline] = useState(() => useLinkedIdentityStore.getState().lastLinkedIdentity);
  const latest = useLinkedIdentityStore((s) => s.lastLinkedIdentity);
  const linked = latest && latest !== baseline ? latest : null;

  if (linked) {
    return (
      <div className="space-y-5">
        <div role="status" className="flex flex-col items-center gap-2 py-4 text-center">
          <CircleCheckIcon className="size-8 text-foreground" aria-hidden="true" />
          <p className="font-semibold">{t("pairing.linked", { name: linked.display })}</p>
          <p className="text-sm text-muted-foreground">
            {t("pairing.linkedBody", { platform: platformName(linked.connector_kind) })}
          </p>
        </div>
        <div className="flex justify-end">
          <Button onClick={onDone}>{t("connect.done")}</Button>
        </div>
      </div>
    );
  }

  if (connectors.isPending) {
    return (
      <div className="space-y-3 py-2" aria-hidden="true">
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-10 w-full" />
      </div>
    );
  }

  if (connectors.isError) {
    return (
      <div role="alert" className="space-y-3 text-sm">
        <p className="text-destructive">{t("pairing.connectorsError")}</p>
        <Button variant="outline" size="sm" onClick={() => connectors.refetch()}>
          {t("common.retry")}
        </Button>
      </div>
    );
  }

  const list = connectors.data;
  if (list.length === 0) {
    return (
      <div className="space-y-3 py-2 text-sm">
        <p className="font-semibold">{t("pairing.noConnectors")}</p>
        <p className="text-muted-foreground">{t("pairing.noConnectorsBody")}</p>
        <Button asChild variant="outline" size="sm">
          <Link to="/connectors" onClick={onDone}>
            {t("pairing.goToConnectors")}
          </Link>
        </Button>
      </div>
    );
  }

  // One usable connector needs no question.
  const target = picked ?? (list.length === 1 ? list[0] : null);
  if (!target) return <ConnectorChoice connectors={list} onChoose={setPicked} />;
  return (
    <PairingCodeView
      key={target.id}
      connector={target}
      onChangeConnector={list.length > 1 ? () => setPicked(null) : undefined}
    />
  );
}

/** Asks which connector reads the chat the code will be sent from (IDN-01, IDN-02). */
function ConnectorChoice({
  connectors,
  onChoose,
}: {
  connectors: Connector[];
  onChoose: (connector: Connector) => void;
}) {
  const t = useT();
  const idBase = useId();
  const orgs = useMyOrgs().data;
  const [selected, setSelected] = useState<string>("");
  const chosen = connectors.find((c) => c.id === selected);
  // The organization owns each connection (AD-016), so that is what tells two of them apart; while the
  // organizations are not known the platform alone is named.
  const kindOf = (c: Connector) => {
    const org = orgs?.find((o) => o.id === c.org_id)?.name;
    return org ? t("pairing.inOrg", { platform: platformName(c.kind), org }) : platformName(c.kind);
  };
  return (
    <div className="space-y-4">
      <ChoiceGroup
        fieldsetClassName="space-y-2"
        legend={t("pairing.chooseConnector")}
        idBase={idBase}
        value={selected}
        onValueChange={setSelected}
        itemClassName="gap-3 py-2.5"
        options={connectors.map((c) => ({
          value: c.id,
          label: (
            <>
              <span className="min-w-0 flex-1 truncate font-medium">{c.name}</span>
              <span className="max-w-[45%] shrink-0 truncate text-xs text-muted-foreground">
                {kindOf(c)}
              </span>
            </>
          ),
        }))}
      />
      <p className="text-xs text-muted-foreground">{t("pairing.chooseHint")}</p>
      <div className="flex justify-end">
        <Button disabled={!chosen} onClick={() => chosen && onChoose(chosen)}>
          {t("pairing.getCode")}
        </Button>
      </div>
    </div>
  );
}

/** Telegram bots see slash commands; elsewhere "!link" is the plain-text form. */
const pairingCommands = (kind: string, code: string) =>
  kind === "telegram"
    ? { command: `/link ${code}`, alt: `!link ${code}` }
    : { command: `!link ${code}`, alt: `/link ${code}` };

function PairingCodeView({
  connector,
  onChangeConnector,
}: {
  connector: Connector;
  onChangeConnector?: () => void;
}) {
  const t = useT();
  const start = useStartPairing();
  const [code, setCode] = useState<PairingCode | null>(null);
  const secondsLeft = useSecondsLeft(code?.expires_at);
  const { mutate } = start;

  const requestCode = () => mutate(connector.id, { onSuccess: setCode });
  // biome-ignore lint/correctness/useExhaustiveDependencies: request one code when the connector is chosen
  useEffect(requestCode, []);

  const through = (
    <p className="flex flex-wrap items-center justify-between gap-2 text-sm text-muted-foreground">
      <span>
        {t("pairing.through", {
          connector: connector.name,
          platform: platformName(connector.kind),
        })}
      </span>
      {onChangeConnector ? (
        <Button variant="link" size="sm" className="h-auto p-0" onClick={onChangeConnector}>
          {t("pairing.changeConnector")}
        </Button>
      ) : null}
    </p>
  );

  if (start.isError) {
    return (
      <div className="space-y-4">
        {through}
        <div role="alert" className="space-y-3 text-sm">
          <p className="text-destructive">{t("pairing.failed")}</p>
          <Button variant="outline" size="sm" onClick={requestCode}>
            {t("common.retry")}
          </Button>
        </div>
      </div>
    );
  }

  if (!code || start.isPending) {
    return (
      <div className="space-y-3 py-2" aria-hidden="true">
        <Skeleton className="mx-auto h-14 w-56" />
        <Skeleton className="h-10 w-full" />
      </div>
    );
  }

  if (secondsLeft === 0) {
    return (
      <div className="space-y-4 py-2 text-center">
        <TimerOffIcon className="mx-auto size-7 text-muted-foreground" aria-hidden="true" />
        <div>
          <p className="font-semibold">{t("pairing.expired")}</p>
          <p className="text-sm text-muted-foreground">{t("pairing.expiredBody")}</p>
        </div>
        <Button onClick={requestCode}>{t("pairing.newCode")}</Button>
      </div>
    );
  }

  const { command, alt } = pairingCommands(connector.kind, code.code);
  return (
    <div className="space-y-5">
      {through}
      <div className="relative flex justify-center py-2">
        <span
          aria-hidden="true"
          className="absolute inset-x-6 top-1/2 h-12 -translate-y-1/2 -rotate-1 rounded-[3px] bg-tape/80 shadow-sm"
        />
        <p className="relative font-mono text-4xl font-semibold tracking-[0.3em] text-tape-foreground tabular">
          <span className="sr-only">{t("pairing.codeLabel")}: </span>
          <span>{code.code}</span>
        </p>
      </div>
      <div className="space-y-2 text-sm text-muted-foreground">
        <p>{t("pairing.send")}</p>
        <CodeBlock label={t("pairing.command")} value={command} />
        <p className="text-xs">{t("pairing.alt", { command: alt })}</p>
      </div>
      <p className="flex items-center justify-between border-t pt-3 text-sm text-muted-foreground">
        <span>{t("pairing.expiresIn")}</span>
        <span
          role="timer"
          aria-live="off"
          className={cn(
            "font-mono font-semibold tabular",
            secondsLeft <= 60
              ? "rounded-[3px] bg-foreground px-1.5 text-background"
              : "text-foreground",
          )}
        >
          {clock(secondsLeft)}
        </span>
      </p>
    </div>
  );
}
