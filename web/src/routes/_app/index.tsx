import { createFileRoute, Link } from "@tanstack/react-router";
import {
  ArrowRightIcon,
  BotIcon,
  MessagesSquareIcon,
  PlugIcon,
  PlugZapIcon,
  PlusIcon,
  RadioTowerIcon,
} from "lucide-react";
import { type ReactNode, useId, useMemo, useRef, useState } from "react";
import { AddConnectorDialog } from "@/components/add-connector-dialog";
import { ConnectAgentDialog } from "@/components/connect-agent-dialog";
import { ConnectorStatusBadge, useConnectorState } from "@/components/connector-status";
import { CreateRoomDialog } from "@/components/create-room-dialog";
import { EmptyState } from "@/components/empty-state";
import {
  AUTHOR_KIND,
  authorDisplayName,
  chatTrust,
  contentKindLabel,
  platformLabel,
  TrustBadge,
} from "@/components/message-item";
import { PageHeader } from "@/components/page-header";
import { StatusDot } from "@/components/status-dot";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useActivity, useAgents, useConnectors, useMe, useRooms } from "@/lib/api/queries";
import type { Connector, ConnectorStatus, Message } from "@/lib/api/types.gen";
import { useI18n, useT } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";
import { type AgentView, byLiveness, withPresence } from "@/lib/presence";
import { formatAbsolute, formatRelative } from "@/lib/time";
import { cn } from "@/lib/utils";

export const Route = createFileRoute("/_app/")({
  component: OverviewPage,
});

/*
 * The overview is the operator's desk: the switchboard (who is on the line, which connectors are
 * up) beside the wire (recent traffic across rooms). Everything on it is
 * live: presence, connector status and new messages patch the cache (lib/live/handlers.ts).
 */

/** Rows shown per side panel before "N more". */
const PANEL_ROWS = 6;

function OverviewPage() {
  const t = useT();
  // Dialogs are owned by the page: their triggers live in empty states that unmount as soon as
  // the first agent or connector appears.
  const [connecting, setConnecting] = useState(false);
  const [adding, setAdding] = useState(false);

  return (
    <>
      <PageHeader
        title={t("page.overviewTitle")}
        description={t("page.overviewBody")}
        actions={
          // biome-ignore lint/a11y/useSemanticElements: a set of page actions, not form fields
          <div
            role="group"
            aria-label={t("overview.quickActions")}
            className="flex flex-wrap gap-2"
          >
            <Button onClick={() => setConnecting(true)}>
              <PlugIcon aria-hidden="true" />
              {t("connect.open")}
            </Button>
            <Button variant="outline" onClick={() => setAdding(true)}>
              <PlugZapIcon aria-hidden="true" />
              {t("connectors.add")}
            </Button>
            <NewRoomButton variant="outline" />
          </div>
        }
      />
      {/* Switchboard first (compact, read at a glance), then the wire. Same order on phones. */}
      <div className="grid lg:grid-cols-[minmax(20rem,25rem)_minmax(0,1fr)]">
        <div className="divide-y border-b lg:border-r lg:border-b-0">
          <AgentsPanel onConnect={() => setConnecting(true)} />
          <ConnectorsPanel onAdd={() => setAdding(true)} />
        </div>
        <ActivityPanel />
      </div>
      <ConnectAgentDialog open={connecting} onOpenChange={setConnecting} />
      <AddConnectorDialog open={adding} onOpenChange={setAdding} />
    </>
  );
}

function NewRoomButton({ variant }: { variant?: "default" | "outline" }) {
  const t = useT();
  return (
    <CreateRoomDialog
      trigger={
        <Button variant={variant}>
          <PlusIcon aria-hidden="true" />
          {t("rooms.new")}
        </Button>
      }
    />
  );
}

// ---------------------------------------------------------------------------------------------
// Panel scaffolding

function Panel({
  title,
  meta,
  link,
  children,
  className,
}: {
  title: string;
  meta?: ReactNode;
  link?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  const id = useId();
  return (
    <section aria-labelledby={id} className={cn("min-w-0 pb-2", className)}>
      <header className="flex min-h-12 items-center justify-between gap-3 px-4 pt-5 pb-2 md:px-8 lg:px-6">
        <div className="flex min-w-0 items-baseline gap-3">
          <h2 id={id} className="label-caps text-[0.72rem] text-muted-foreground">
            {title}
          </h2>
          {meta}
        </div>
        {link}
      </header>
      {children}
    </section>
  );
}

function PanelLink({
  to,
  children,
}: {
  to: "/rooms" | "/agents" | "/connectors";
  children: string;
}) {
  return (
    <Link
      to={to}
      className="group inline-flex shrink-0 items-center gap-1 rounded-sm text-xs font-medium text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50"
    >
      {children}
      <ArrowRightIcon
        className="size-3.5 transition-transform group-hover:translate-x-0.5"
        aria-hidden="true"
      />
    </Link>
  );
}

function PanelError({ onRetry }: { onRetry: () => void }) {
  const t = useT();
  return (
    <div className="px-4 py-6 text-sm md:px-8 lg:px-6" role="alert">
      <p className="text-destructive">{t("overview.loadError")}</p>
      <Button variant="outline" size="sm" className="mt-3" onClick={onRetry}>
        {t("common.retry")}
      </Button>
    </div>
  );
}

function RowsSkeleton({ rows }: { rows: number }) {
  return (
    <ul aria-hidden="true" className="space-y-4 px-4 py-3 md:px-8 lg:px-6">
      {Array.from({ length: rows }, (_, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: static placeholder rows
        <li key={i} className="space-y-2">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-3 w-64 max-w-full" />
        </li>
      ))}
    </ul>
  );
}

/** Compact empty state for a panel: same voice as the page-level one, less air. */
function PanelEmpty(props: Parameters<typeof EmptyState>[0]) {
  return <EmptyState {...props} headingLevel="h3" className="py-8 md:py-10" />;
}

function MoreRow({ count, label }: { count: number; label: string }) {
  if (count <= 0) return null;
  return <p className="px-4 pt-1 pb-2 text-xs text-muted-foreground md:px-8 lg:px-6">{label}</p>;
}

// ---------------------------------------------------------------------------------------------
// Recent activity: the wire

/** One-line preview of agent Markdown: code blocks folded, links and emphasis reduced to text. */
function plainPreview(text: string): string {
  return text
    .replace(/```[\s\S]*?(```|$)/g, " ")
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/^\s{0,3}(#{1,6}|>|[-*+]|\d+\.)\s+/gm, "")
    .replace(/(\*\*|__|\*|_|`)/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

function ActivityPanel() {
  const { t } = useI18n();
  const rooms = useRooms();
  const activity = useActivity();
  // Messages that arrive after the first load land on fresh tape.
  const known = useRef<Set<string> | null>(null);
  if (activity.data && known.current === null) {
    known.current = new Set(activity.data.map((a) => a.message.id));
  }

  let body: ReactNode;
  if (rooms.isPending || (activity.isPending && (rooms.data?.length ?? 0) > 0)) {
    body = <RowsSkeleton rows={4} />;
  } else if (rooms.isSuccess && rooms.data.length === 0) {
    // No rooms: nothing can be on the wire, whatever the feed request did.
    body = (
      <PanelEmpty
        icon={MessagesSquareIcon}
        title={t("overview.noRoomsTitle")}
        action={<NewRoomButton />}
      >
        <p>{t("overview.noRoomsBody")}</p>
      </PanelEmpty>
    );
  } else if (rooms.isError || activity.isError) {
    body = (
      <PanelError
        onRetry={() => {
          void rooms.refetch();
          void activity.refetch();
        }}
      />
    );
  } else if (!activity.data?.length) {
    body = (
      <PanelEmpty
        icon={RadioTowerIcon}
        title={t("overview.quietTitle")}
        action={
          <Button variant="outline" asChild>
            <Link to="/rooms">{t("overview.openRooms")}</Link>
          </Button>
        }
      >
        <p>{t("overview.quietBody")}</p>
      </PanelEmpty>
    );
  } else {
    body = (
      <ol aria-label={t("overview.activityLabel")} className="pb-2">
        {activity.data.map(({ room, message }) => (
          <ActivityRow
            key={message.id}
            message={message}
            room={room.name || t("overview.unknownRoom")}
            fresh={known.current !== null && !known.current.has(message.id)}
          />
        ))}
      </ol>
    );
  }

  return (
    <Panel
      title={t("overview.activityTitle")}
      link={rooms.data?.length ? <PanelLink to="/rooms">{t("overview.allRooms")}</PanelLink> : null}
    >
      {body}
    </Panel>
  );
}

function ActivityRow({ message, room, fresh }: { message: Message; room: string; fresh: boolean }) {
  const { t, locale } = useI18n();
  const me = useMe().data;
  const kind = AUTHOR_KIND[message.author_kind] ?? AUTHOR_KIND.system;
  const placeholder = contentKindLabel(message.kind);
  return (
    <li className={cn(fresh && "animate-tape-settle")}>
      <Link
        to="/rooms/$roomId"
        params={{ roomId: message.room_id }}
        className="group grid grid-cols-[1.75rem_minmax(0,1fr)_auto] gap-x-3 px-4 py-2.5 outline-none transition-colors hover:bg-accent/60 focus-visible:bg-accent focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:ring-inset md:px-8 lg:px-6"
      >
        <span
          aria-hidden="true"
          className={cn("mt-0.5 flex size-7 items-center justify-center rounded-md", kind.tile)}
        >
          <kind.icon className="size-3.5" strokeWidth={1.9} />
        </span>
        <span className="min-w-0">
          <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
            <span className="sr-only">{t(kind.label)}</span>
            <span className="truncate font-semibold tracking-[-0.01em]">
              {authorDisplayName(message)}
            </span>
            <span className="inline-flex min-w-0 items-center gap-1 text-xs text-muted-foreground">
              <MessagesSquareIcon className="size-3 shrink-0" aria-hidden="true" />
              <span className="sr-only">{t("overview.inRoom")}</span>
              <span className="truncate font-medium">{room}</span>
            </span>
            {message.author_kind === "chat" && me ? (
              <TrustBadge trust={chatTrust(message, me.id)} />
            ) : null}
          </span>
          <span className="mt-0.5 line-clamp-2 text-sm break-words text-foreground/85">
            {(message.author_kind === "agent" ? plainPreview(message.text) : message.text) ||
              (placeholder ? (
                <span className="text-muted-foreground italic">{t(placeholder)}</span>
              ) : null)}
          </span>
        </span>
        <time
          dateTime={message.created_at}
          title={formatAbsolute(message.created_at, locale)}
          className="pt-0.5 font-mono text-[0.7rem] text-muted-foreground tabular"
        >
          {formatRelative(message.created_at, locale)}
        </time>
      </Link>
    </li>
  );
}

// ---------------------------------------------------------------------------------------------
// Agents: who is on the line

function AgentsPanel({ onConnect }: { onConnect: () => void }) {
  const { t } = useI18n();
  const agents = useAgents();
  const presence = useLiveStore((s) => s.presence);
  const sorted = useMemo(
    () => (agents.data ?? []).map((a) => withPresence(a, presence[a.id])).sort(byLiveness),
    [agents.data, presence],
  );
  const online = sorted.filter((a) => a.live).length;

  let body: ReactNode;
  if (agents.isPending) body = <RowsSkeleton rows={2} />;
  else if (agents.isError) body = <PanelError onRetry={() => agents.refetch()} />;
  else if (sorted.length === 0) {
    body = (
      <PanelEmpty
        icon={BotIcon}
        title={t("overview.agentsEmptyTitle")}
        action={
          <Button onClick={onConnect}>
            <PlugIcon aria-hidden="true" />
            {t("connect.open")}
          </Button>
        }
      >
        <p>{t("overview.agentsEmptyBody")}</p>
      </PanelEmpty>
    );
  } else {
    body = (
      <>
        <ul className="pb-1">
          {sorted.slice(0, PANEL_ROWS).map((agent) => (
            <AgentLine key={agent.id} agent={agent} />
          ))}
        </ul>
        <MoreRow
          count={sorted.length - PANEL_ROWS}
          label={t("overview.moreAgents", { count: sorted.length - PANEL_ROWS })}
        />
      </>
    );
  }

  return (
    <Panel
      title={t("overview.agentsTitle")}
      meta={
        sorted.length > 0 ? (
          <span className="font-mono text-xs text-foreground/80 tabular">
            {t("overview.agentsOnline", { online, total: sorted.length })}
          </span>
        ) : null
      }
      link={
        sorted.length > 0 ? <PanelLink to="/agents">{t("overview.allAgents")}</PanelLink> : null
      }
    >
      {body}
    </Panel>
  );
}

function AgentLine({ agent }: { agent: AgentView }) {
  const { t, locale } = useI18n();
  return (
    <li
      aria-label={agent.name}
      className="flex items-center justify-between gap-3 px-4 py-2 md:px-8 lg:px-6"
    >
      <span className="min-w-0">
        <span className="flex min-w-0 items-center gap-2">
          <BotIcon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <span className="truncate font-medium">{agent.name}</span>
        </span>
        {agent.live ? null : (
          <span
            className="block truncate pl-6 font-mono text-[0.7rem] text-muted-foreground tabular"
            title={agent.seen ? formatAbsolute(agent.seen, locale) : undefined}
          >
            {agent.seen
              ? t("agents.lastSeen", { time: formatRelative(agent.seen, locale) })
              : t("agents.neverSeen")}
          </span>
        )}
      </span>
      <StatusDot online={agent.live} withLabel className="shrink-0" />
    </li>
  );
}

// ---------------------------------------------------------------------------------------------
// Connectors: health of every platform account

const ATTENTION: Record<ConnectorStatus, number> = {
  error: 0,
  connecting: 1,
  connected: 2,
  stopped: 3,
};

function ConnectorsPanel({ onAdd }: { onAdd: () => void }) {
  const { t } = useI18n();
  const connectors = useConnectors();
  const live = useLiveStore((s) => s.connectors);
  const sorted = useMemo(() => {
    const status = (c: Connector) => live[c.id]?.status ?? c.status;
    return [...(connectors.data ?? [])].sort(
      (a, b) => ATTENTION[status(a)] - ATTENTION[status(b)] || a.name.localeCompare(b.name),
    );
  }, [connectors.data, live]);
  const connected = sorted.filter((c) => (live[c.id]?.status ?? c.status) === "connected").length;

  let body: ReactNode;
  if (connectors.isPending) body = <RowsSkeleton rows={2} />;
  else if (connectors.isError) body = <PanelError onRetry={() => connectors.refetch()} />;
  else if (sorted.length === 0) {
    body = (
      <PanelEmpty
        icon={PlugZapIcon}
        title={t("overview.connectorsEmptyTitle")}
        action={
          <Button onClick={onAdd}>
            <PlusIcon aria-hidden="true" />
            {t("connectors.add")}
          </Button>
        }
      >
        <p>{t("overview.connectorsEmptyBody")}</p>
      </PanelEmpty>
    );
  } else {
    body = (
      <>
        <ul className="pb-1">
          {sorted.slice(0, PANEL_ROWS).map((c) => (
            <ConnectorLine key={c.id} connector={c} />
          ))}
        </ul>
        <MoreRow
          count={sorted.length - PANEL_ROWS}
          label={t("overview.moreConnectors", { count: sorted.length - PANEL_ROWS })}
        />
      </>
    );
  }

  return (
    <Panel
      title={t("overview.connectorsTitle")}
      meta={
        sorted.length > 0 ? (
          <span className="font-mono text-xs text-foreground/80 tabular">
            {t("overview.connectorsConnected", { connected, total: sorted.length })}
          </span>
        ) : null
      }
      link={
        sorted.length > 0 ? (
          <PanelLink to="/connectors">{t("overview.allConnectors")}</PanelLink>
        ) : null
      }
    >
      {body}
    </Panel>
  );
}

function ConnectorLine({ connector: c }: { connector: Connector }) {
  const { status, lastError } = useConnectorState(c);
  return (
    <li aria-label={c.name}>
      <Link
        to="/connectors/$connectorId"
        params={{ connectorId: c.id }}
        className="flex flex-col gap-0.5 px-4 py-2 outline-none transition-colors hover:bg-accent/60 focus-visible:bg-accent focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:ring-inset md:px-8 lg:px-6"
      >
        <span className="flex items-center justify-between gap-3">
          <span className="flex min-w-0 items-center gap-2">
            <span className="truncate font-medium">{c.name}</span>
            <span className="shrink-0 rounded-[3px] border px-1.5 py-px text-[0.68rem] text-muted-foreground">
              {platformLabel(c.kind)}
            </span>
          </span>
          <ConnectorStatusBadge status={status} enabled={c.enabled} className="shrink-0" />
        </span>
        {lastError && status === "error" ? (
          <span className="truncate font-mono text-[0.7rem] text-destructive" title={lastError}>
            {lastError}
          </span>
        ) : null}
      </Link>
    </li>
  );
}
