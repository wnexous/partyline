import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import {
  ArrowLeftIcon,
  MessageCircleIcon,
  PlugZapIcon,
  RefreshCwIcon,
  Settings2Icon,
  Share2Icon,
  TriangleAlertIcon,
} from "lucide-react";
import { type ReactNode, useId, useState } from "react";
import { toast } from "sonner";
import { ConnectorStatusBadge, useConnectorState } from "@/components/connector-status";
import { CodeBlock } from "@/components/copy-field";
import { EmptyState } from "@/components/empty-state";
import { FormAlert } from "@/components/form-fields";
import { chatTitle, platformLabel } from "@/components/message-item";
import { NativeSelect } from "@/components/native-select";
import { NamedSentence } from "@/components/profile-link";
import { ConnectorForm } from "@/components/schema-form";
import { SharingDialog } from "@/components/sharing-dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { isApiError } from "@/lib/api/client";
import {
  useConnector,
  useConnectorChats,
  useConnectorKinds,
  useContacts,
  useDeleteConnector,
  useMe,
  useRefreshChats,
  useUpdateConnector,
} from "@/lib/api/queries";
import {
  type Connector,
  type ConnectorDeliver,
  ConnectorDeliverAll,
  ConnectorDeliverDMs,
  ConnectorDeliverGroups,
  type ConnectorKind,
  LevelManage,
  type UpdateConnectorRequest,
} from "@/lib/api/types.gen";
import { flattenTree } from "@/lib/contacts";
import { type MessageKey, useI18n, useT } from "@/lib/i18n";
import { formatAbsolute, formatRelative } from "@/lib/time";

export const Route = createFileRoute("/_app/connectors/$connectorId")({
  component: ConnectorPage,
});

const CHAT_KIND: Record<string, MessageKey> = {
  group: "members.chatKind.group",
  dm: "members.chatKind.dm",
  channel: "members.chatKind.channel",
};

/** The delivery choices in the order the select offers them (CID-12, CID-27). */
const DELIVER: Record<ConnectorDeliver, MessageKey> = {
  all: "connector.deliver.all",
  dms: "connector.deliver.dms",
  groups: "connector.deliver.groups",
};
const DELIVER_OPTIONS: ConnectorDeliver[] = [
  ConnectorDeliverAll,
  ConnectorDeliverDMs,
  ConnectorDeliverGroups,
];

function ConnectorPage() {
  const t = useT();
  const { connectorId } = Route.useParams();
  const connector = useConnector(connectorId);
  const kinds = useConnectorKinds();

  if (connector.isError && isApiError(connector.error) && connector.error.status === 404) {
    return (
      <EmptyState
        icon={PlugZapIcon}
        title={t("connector.notFoundTitle")}
        action={
          <Button asChild variant="outline">
            <Link to="/connectors">{t("connector.back")}</Link>
          </Button>
        }
      >
        <p>{t("connector.notFoundBody")}</p>
      </EmptyState>
    );
  }
  if (!connector.data) {
    return (
      <div className="space-y-3 px-4 pt-8 md:px-8" aria-hidden="true">
        <Skeleton className="h-6 w-48" />
        <Skeleton className="h-4 w-72 max-w-full" />
      </div>
    );
  }
  const kind = kinds.data?.find((k) => k.name === connector.data.kind);
  return <ConnectorView connector={connector.data} kind={kind} />;
}

function ConnectorView({ connector: c, kind }: { connector: Connector; kind?: ConnectorKind }) {
  const t = useT();
  const me = useMe().data;
  const canManage = c.my_level === LevelManage;
  const { status, lastError } = useConnectorState(c);
  const update = useUpdateConnector(c.id);
  const foreign = me && c.owner.id !== me.id;
  const enabledId = useId();

  const setEnabled = (enabled: boolean) =>
    update.mutate({ enabled }, { onError: () => toast.error(t("connector.updateFailed")) });

  return (
    <>
      <header className="border-b px-4 pt-5 pb-5 md:px-8 md:pt-6">
        <Link
          to="/connectors"
          className="mb-3 inline-flex items-center gap-1 rounded-sm text-sm text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/70"
        >
          <ArrowLeftIcon className="size-3.5" aria-hidden="true" />
          {t("page.connectorsTitle")}
        </Link>
        <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
          <div className="min-w-0 space-y-1.5">
            <h1 className="truncate text-2xl font-semibold tracking-[-0.02em]">{c.name}</h1>
            <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-muted-foreground">
              <span>{kind?.label ?? platformLabel(c.kind)}</span>
              <ConnectorStatusBadge status={status} enabled={c.enabled} />
              {c.operator ? (
                <span>
                  <NamedSentence
                    id={c.operator.id}
                    name={c.operator.display_name}
                    sentence={(name) => t("connector.operatedBy", { name })}
                  />
                </span>
              ) : (
                <span className="font-medium text-destructive">{t("connector.noOperator")}</span>
              )}
              <span title={t("connector.queuedHint")}>
                {t("connector.queued", { count: c.queued })}
              </span>
              {foreign ? (
                <span className="text-xs">
                  {t("rooms.ownedBy", { username: c.owner.username })}
                </span>
              ) : null}
            </p>
          </div>
          {canManage ? (
            <div className="flex shrink-0 items-center gap-1">
              <SharingDialog
                kind="connectors"
                id={c.id}
                name={c.name}
                owner={c.owner}
                trigger={
                  <Button variant="outline" size="sm">
                    <Share2Icon aria-hidden="true" />
                    {t("room.share")}
                  </Button>
                }
              />
              <ConnectorSettingsSheet connector={c} kind={kind} />
            </div>
          ) : null}
        </div>
      </header>

      <div className="max-w-3xl space-y-8 px-4 py-6 md:px-8">
        {lastError ? (
          <div
            role="status"
            className="flex gap-2.5 rounded-md bg-destructive/8 px-3 py-2.5 text-sm ring-1 ring-destructive/30 ring-inset"
          >
            <TriangleAlertIcon
              className="mt-0.5 size-4 shrink-0 text-destructive"
              aria-hidden="true"
            />
            <div className="min-w-0">
              <p className="font-medium text-destructive">{t("connector.lastError")}</p>
              <p className="mt-0.5 font-mono text-xs break-words text-foreground">{lastError}</p>
            </div>
          </div>
        ) : null}

        {canManage ? (
          <div className="flex items-start justify-between gap-4 rounded-md border px-4 py-3">
            <span className="space-y-0.5">
              <label htmlFor={enabledId} className="block text-sm font-medium">
                {t("connector.enabled")}
              </label>
              <span className="block text-xs text-muted-foreground">
                {t("connector.enabledHint")}
              </span>
            </span>
            <Switch
              checked={c.enabled}
              onCheckedChange={setEnabled}
              disabled={update.isPending}
              id={enabledId}
            />
          </div>
        ) : null}

        <DeliverySection connector={c} canManage={canManage} />

        {c.inbound_url ? (
          <Section title={t("connector.inboundTitle")}>
            <p className="text-sm text-muted-foreground">{t("connector.inboundBody")}</p>
            <CodeBlock label={t("connector.inboundUrl")} value={c.inbound_url} />
          </Section>
        ) : null}

        <ChatsSection connector={c} canList={kind?.can_list_chats ?? false} />
      </div>
    </>
  );
}

function Section({
  title,
  actions,
  children,
}: {
  title: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <h2 className="label-caps text-[0.72rem] text-muted-foreground">{title}</h2>
        {actions}
      </div>
      {children}
    </section>
  );
}

function ChatsSection({ connector: c, canList }: { connector: Connector; canList: boolean }) {
  const { t, locale } = useI18n();
  const chats = useConnectorChats(c.id);
  const refresh = useRefreshChats(c.id);
  const list = chats.data ?? [];

  return (
    <Section
      title={
        <>
          {t("connector.chats")}{" "}
          {chats.data ? <span className="font-mono tabular">{list.length}</span> : null}
        </>
      }
      actions={
        canList ? (
          <Button
            variant="ghost"
            size="sm"
            disabled={refresh.isPending}
            onClick={() =>
              refresh.mutate(undefined, {
                onError: () => toast.error(t("connector.refreshFailed")),
              })
            }
          >
            <RefreshCwIcon
              className={refresh.isPending ? "animate-spin" : undefined}
              aria-hidden="true"
            />
            {t("connector.refresh")}
          </Button>
        ) : null
      }
    >
      {chats.isPending ? (
        <Skeleton className="h-16 w-full" />
      ) : list.length === 0 ? (
        <p className="rounded-md border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
          {t("connector.noChats")}
        </p>
      ) : (
        <ul aria-label={t("connector.chats")} className="divide-y rounded-md border">
          {list.map((chat) => {
            const name = chatTitle(t, chat.title, chat.connector_kind);
            return (
              <li
                key={chat.id}
                aria-label={name}
                className="flex items-center gap-3 px-3 py-2.5 sm:px-4"
              >
                <MessageCircleIcon
                  className="size-4 shrink-0 text-muted-foreground"
                  aria-hidden="true"
                />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">{name}</p>
                  <p className="flex min-w-0 flex-wrap gap-x-2 text-xs text-muted-foreground">
                    <span>
                      {CHAT_KIND[chat.kind] ? t(CHAT_KIND[chat.kind] as MessageKey) : chat.kind}
                    </span>
                    <span className="truncate font-mono">{chat.external_id}</span>
                    {chat.last_seen_at ? (
                      <span
                        className="font-mono tabular"
                        title={formatAbsolute(chat.last_seen_at, locale)}
                      >
                        {formatRelative(chat.last_seen_at, locale)}
                      </span>
                    ) : null}
                  </p>
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </Section>
  );
}

function ConnectorSettingsSheet({
  connector: c,
  kind,
}: {
  connector: Connector;
  kind?: ConnectorKind;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const update = useUpdateConnector(c.id);
  const fields = kind ?? { name: c.kind, label: c.kind, fields: [], can_list_chats: false };

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label={t("connector.settings")}>
          <Settings2Icon aria-hidden="true" />
        </Button>
      </SheetTrigger>
      <SheetContent className="w-full gap-0 sm:max-w-md">
        <SheetHeader className="border-b">
          <SheetTitle>{t("connector.settings")}</SheetTitle>
          <SheetDescription>{t("connector.settingsBody")}</SheetDescription>
        </SheetHeader>
        {open ? (
          <div className="min-h-0 flex-1 space-y-8 overflow-y-auto p-4">
            <ConnectorForm
              kind={fields}
              initial={{ name: c.name, config: c.config }}
              submitLabel={t("common.save")}
              pending={update.isPending}
              onSubmit={(values, form) =>
                update.mutate(values, {
                  onSuccess: () => {
                    toast.success(t("connector.saved"));
                    setOpen(false);
                  },
                  onError: (err) => {
                    if (
                      isApiError(err) &&
                      err.status === 422 &&
                      form.applyServerErrors(err.fields)
                    ) {
                      return;
                    }
                    form.setFormError(
                      isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic"),
                    );
                  },
                })
              }
            />
            <DeleteConnector connector={c} />
          </div>
        ) : null}
      </SheetContent>
    </Sheet>
  );
}

/**
 * The proxy shape of a connection (CID-23): the AI that operates the account, and what reaches it.
 * Only the owner changes them; a use grantee reads the same values without controls.
 */
function DeliverySection({
  connector: c,
  canManage,
}: {
  connector: Connector;
  canManage: boolean;
}) {
  const t = useT();
  return (
    <Section title={t("connector.delivery")}>
      <p className="text-sm text-muted-foreground">{t("connector.deliveryBody")}</p>
      {canManage ? <DeliveryForm connector={c} /> : <DeliverySummary connector={c} />}
    </Section>
  );
}

/** The owner's form: the operator (one of their AIs) and the delivery filters (CID-27). */
function DeliveryForm({ connector: c }: { connector: Connector }) {
  const t = useT();
  const update = useUpdateConnector(c.id);
  const contacts = useContacts();
  const id = useId();
  const current = c.operator?.id ?? "";
  const [operatorId, setOperatorId] = useState(current);
  const [deliver, setDeliver] = useState<ConnectorDeliver>(c.deliver);
  const [mentionsOnly, setMentionsOnly] = useState(c.groups_mentions_only);
  const [error, setError] = useState<string | null>(null);
  const [operatorError, setOperatorError] = useState<string | null>(null);
  const [deliverError, setDeliverError] = useState<string | null>(null);
  const own = contacts.data
    ? [...contacts.data.machines.flatMap((m) => flattenTree(m.nodes)), ...contacts.data.unlinked]
    : [];
  const missing = current !== "" && !own.some((a) => a.intelligence.id === current);
  const dirty =
    operatorId !== current || deliver !== c.deliver || mentionsOnly !== c.groups_mentions_only;
  const operatorField = `${id}-operator`;
  const operatorHint = `${id}-operator-hint`;
  const deliverField = `${id}-deliver`;
  const deliverHint = `${id}-deliver-hint`;
  const mentionsField = `${id}-mentions`;

  const save = () => {
    if (!dirty || update.isPending) return;
    setError(null);
    setOperatorError(null);
    setDeliverError(null);
    // Only the changed fields go: the server leaves an absent one alone.
    const req: UpdateConnectorRequest = {};
    if (operatorId !== current) req.operator_id = operatorId;
    if (deliver !== c.deliver) req.deliver = deliver;
    if (mentionsOnly !== c.groups_mentions_only) req.groups_mentions_only = mentionsOnly;
    update.mutate(req, {
      onSuccess: () => toast.success(t("connector.deliverySaved")),
      onError: (err) => {
        if (isApiError(err) && err.status === 422) {
          if (err.fields.operator_id) {
            setOperatorError(err.fields.operator_id);
            return;
          }
          if (err.fields.deliver) {
            setDeliverError(err.fields.deliver);
            return;
          }
        }
        setError(
          isApiError(err) && err.status === 403
            ? t("connector.operatorForbidden")
            : t("connector.deliveryFailed"),
        );
      },
    });
  };

  return (
    <div className="space-y-4">
      <FormAlert>{error}</FormAlert>
      {current === "" ? (
        <p
          role="note"
          className="rounded-md border border-tape-strong/40 bg-muted px-3 py-2 text-sm"
        >
          {t("connector.noOperatorHint")}
        </p>
      ) : null}
      <div className="space-y-1">
        <label htmlFor={operatorField} className="block text-sm font-medium">
          {t("connector.operatorPick")}
        </label>
        {contacts.isPending ? (
          <Skeleton className="h-9 w-full" />
        ) : contacts.isError ? (
          <p role="alert" className="text-sm text-destructive">
            {t("connector.aiLoadError")}
          </p>
        ) : (
          <NativeSelect
            id={operatorField}
            value={operatorId}
            onChange={(e) => {
              setOperatorId(e.target.value);
              setOperatorError(null);
            }}
            aria-invalid={operatorError ? true : undefined}
            aria-describedby={operatorError ? `${operatorField}-error` : operatorHint}
          >
            <option value="" disabled>
              {t("connector.operatorNone")}
            </option>
            {missing ? (
              <option value={current}>{c.operator?.display_name ?? current}</option>
            ) : null}
            {own.map((a) => (
              <option key={a.intelligence.id} value={a.intelligence.id}>
                {a.intelligence.display_name} (@{a.intelligence.handle})
              </option>
            ))}
          </NativeSelect>
        )}
        {operatorError ? (
          <p id={`${operatorField}-error`} role="alert" className="text-sm text-destructive">
            {operatorError}
          </p>
        ) : (
          <p id={operatorHint} className="text-xs text-muted-foreground">
            {t("connector.operatorHint")}
          </p>
        )}
      </div>
      <div className="space-y-1">
        <label htmlFor={deliverField} className="block text-sm font-medium">
          {t("connector.deliver")}
        </label>
        <NativeSelect
          id={deliverField}
          value={deliver}
          onChange={(e) => {
            setDeliver(e.target.value as ConnectorDeliver);
            setDeliverError(null);
          }}
          aria-invalid={deliverError ? true : undefined}
          aria-describedby={deliverError ? `${deliverField}-error` : deliverHint}
        >
          {DELIVER_OPTIONS.map((value) => (
            <option key={value} value={value}>
              {t(DELIVER[value])}
            </option>
          ))}
        </NativeSelect>
        {deliverError ? (
          <p id={`${deliverField}-error`} role="alert" className="text-sm text-destructive">
            {deliverError}
          </p>
        ) : (
          <p id={deliverHint} className="text-xs text-muted-foreground">
            {t("connector.deliverHint")}
          </p>
        )}
      </div>
      <div className="flex items-start justify-between gap-4 rounded-md border px-4 py-3">
        <span className="space-y-0.5">
          <label htmlFor={mentionsField} className="block text-sm font-medium">
            {t("connector.mentionsOnly")}
          </label>
          <span className="block text-xs text-muted-foreground">
            {t("connector.mentionsOnlyHint")}
          </span>
        </span>
        <Switch
          id={mentionsField}
          checked={mentionsOnly}
          onCheckedChange={setMentionsOnly}
          disabled={update.isPending}
        />
      </div>
      <div className="flex justify-end">
        <Button size="sm" disabled={!dirty || update.isPending} onClick={save}>
          {t("connector.saveDelivery")}
        </Button>
      </div>
    </div>
  );
}

/** What the connection does, for someone the owner shared it with: no controls (CID-23). */
function DeliverySummary({ connector: c }: { connector: Connector }) {
  const t = useT();
  return (
    <div className="space-y-3">
      {c.operator ? null : (
        <p
          role="note"
          className="rounded-md border border-tape-strong/40 bg-muted px-3 py-2 text-sm"
        >
          {t("connector.noOperatorReadOnly")}
        </p>
      )}
      <dl className="grid gap-3 text-sm sm:grid-cols-2">
        <div className="space-y-0.5">
          <dt className="text-xs font-medium text-muted-foreground">
            {t("connector.operatorPick")}
          </dt>
          <dd>
            {c.operator ? (
              <NamedSentence
                id={c.operator.id}
                name={c.operator.display_name}
                sentence={(name) =>
                  t("connector.operatorNamed", { name, handle: c.operator?.handle ?? "" })
                }
              />
            ) : (
              t("connector.noOperator")
            )}
          </dd>
        </div>
        <div className="space-y-0.5">
          <dt className="text-xs font-medium text-muted-foreground">{t("connector.deliver")}</dt>
          <dd>{t(DELIVER[c.deliver])}</dd>
        </div>
        <div className="space-y-0.5">
          <dt className="text-xs font-medium text-muted-foreground">
            {t("connector.mentionsOnly")}
          </dt>
          <dd>{c.groups_mentions_only ? t("common.yes") : t("common.no")}</dd>
        </div>
      </dl>
    </div>
  );
}

function DeleteConnector({ connector: c }: { connector: Connector }) {
  const t = useT();
  const navigate = useNavigate();
  const remove = useDeleteConnector();
  return (
    <div className="space-y-2 border-t pt-5">
      <p className="text-sm text-muted-foreground">{t("connector.deleteHint")}</p>
      <AlertDialog>
        <AlertDialogTrigger asChild>
          <Button variant="outline" className="text-destructive hover:text-destructive">
            {t("connector.delete")}
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("connector.deleteTitle", { name: c.name })}</AlertDialogTitle>
            <AlertDialogDescription>{t("connector.deleteBody")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              disabled={remove.isPending}
              onClick={(e) => {
                e.preventDefault();
                remove.mutate(c.id, {
                  onSuccess: () => {
                    toast(t("connector.deleted", { name: c.name }));
                    void navigate({ to: "/connectors" });
                  },
                  onError: () => toast.error(t("connector.deleteFailed")),
                });
              }}
            >
              {t("connector.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
