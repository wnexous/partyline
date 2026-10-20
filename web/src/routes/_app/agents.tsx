import { createFileRoute } from "@tanstack/react-router";
import { BotIcon, PlugIcon, Share2Icon, Trash2Icon } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";
import { ConnectAgentDialog } from "@/components/connect-agent-dialog";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { SharingDialog } from "@/components/sharing-dialog";
import { StatusDot } from "@/components/status-dot";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { useAgents, useDeleteAgent, useMe } from "@/lib/api/queries";
import { type Agent, LevelManage } from "@/lib/api/types.gen";
import { useI18n } from "@/lib/i18n";
import { type Presence, useLiveStore } from "@/lib/live/store";
import { formatAbsolute, formatRelative } from "@/lib/time";

export const Route = createFileRoute("/_app/agents")({
  component: AgentsPage,
});

interface AgentView extends Agent {
  live: boolean;
  seen?: string;
}

function withPresence(agent: Agent, presence: Presence | undefined): AgentView {
  return {
    ...agent,
    live: presence?.online ?? agent.online,
    seen: presence?.last_seen_at ?? agent.last_seen_at,
  };
}

/** Online first, then most recently seen, then never connected; ties by name. */
function byLiveness(a: AgentView, b: AgentView) {
  if (a.live !== b.live) return a.live ? -1 : 1;
  const seenA = a.seen ? Date.parse(a.seen) : 0;
  const seenB = b.seen ? Date.parse(b.seen) : 0;
  if (seenA !== seenB) return seenB - seenA;
  return a.name.localeCompare(b.name);
}

function AgentsPage() {
  const { t } = useI18n();
  const agents = useAgents();
  const presence = useLiveStore((s) => s.presence);
  const [deleting, setDeleting] = useState<Agent | null>(null);
  const sorted = useMemo(
    () => (agents.data ?? []).map((a) => withPresence(a, presence[a.id])).sort(byLiveness),
    [agents.data, presence],
  );

  const [connecting, setConnecting] = useState(false);
  const connect = (
    <Button onClick={() => setConnecting(true)}>
      <PlugIcon aria-hidden="true" />
      {t("connect.open")}
    </Button>
  );

  return (
    <>
      <PageHeader
        title={t("page.agentsTitle")}
        description={t("page.agentsBody")}
        actions={sorted.length > 0 ? connect : null}
      />
      {agents.isPending ? (
        <ListSkeleton />
      ) : agents.isError ? (
        <div className="px-4 py-10 text-sm md:px-8" role="alert">
          <p className="text-destructive">{t("agents.loadError")}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => agents.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : sorted.length === 0 ? (
        <EmptyState icon={BotIcon} title={t("agents.emptyTitle")} action={connect}>
          <p>{t("agents.emptyBody")}</p>
        </EmptyState>
      ) : (
        <ul aria-label={t("agents.listLabel")} className="divide-y border-b">
          {sorted.map((agent) => (
            <AgentRow key={agent.id} agent={agent} onDelete={() => setDeleting(agent)} />
          ))}
        </ul>
      )}
      <ConnectAgentDialog open={connecting} onOpenChange={setConnecting} />
      <DeleteAgentDialog agent={deleting} onClose={() => setDeleting(null)} />
    </>
  );
}

function AgentRow({ agent, onDelete }: { agent: AgentView; onDelete: () => void }) {
  const { t, locale } = useI18n();
  const me = useMe().data;
  const foreign = me && agent.owner.id !== me.id;
  const canManage = agent.my_level === LevelManage;

  return (
    <li
      aria-label={agent.name}
      className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 px-4 py-3.5 md:px-8"
    >
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-2">
          <BotIcon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <span className="truncate font-semibold tracking-[-0.01em]">{agent.name}</span>
          {foreign ? (
            <span className="shrink-0 text-xs text-muted-foreground">
              {t("rooms.ownedBy", { username: agent.owner.username })}
            </span>
          ) : null}
        </div>
        <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 pl-6 text-sm text-muted-foreground">
          <StatusDot online={agent.live} withLabel />
          {agent.live ? null : agent.seen ? (
            <span className="font-mono text-xs tabular" title={formatAbsolute(agent.seen, locale)}>
              {t("agents.lastSeen", { time: formatRelative(agent.seen, locale) })}
            </span>
          ) : (
            <span className="text-xs">{t("agents.neverSeen")}</span>
          )}
          {agent.description ? <span className="min-w-0 truncate">{agent.description}</span> : null}
        </div>
      </div>
      {canManage ? (
        <div className="flex items-center gap-1">
          <SharingDialog
            kind="agents"
            id={agent.id}
            name={agent.name}
            owner={agent.owner}
            trigger={
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={t("agents.share", { name: agent.name })}
                title={t("room.share")}
              >
                <Share2Icon aria-hidden="true" />
              </Button>
            }
          />
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={t("agents.delete", { name: agent.name })}
            title={t("agents.deleteShort")}
            className="text-muted-foreground hover:text-destructive"
            onClick={onDelete}
          >
            <Trash2Icon aria-hidden="true" />
          </Button>
        </div>
      ) : null}
    </li>
  );
}

function DeleteAgentDialog({ agent, onClose }: { agent: Agent | null; onClose: () => void }) {
  const { t } = useI18n();
  const remove = useDeleteAgent();
  const confirm = () => {
    if (!agent) return;
    remove.mutate(agent.id, {
      onSuccess: () => {
        toast(t("agents.deleted", { name: agent.name }));
        onClose();
      },
      onError: () => toast.error(t("agents.deleteFailed", { name: agent.name })),
    });
  };
  return (
    <Dialog open={agent !== null} onOpenChange={(open) => (open ? null : onClose())}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("agents.deleteTitle", { name: agent?.name ?? "" })}</DialogTitle>
          <DialogDescription>{t("agents.deleteBody")}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button variant="destructive" onClick={confirm} disabled={remove.isPending}>
            {t("agents.deleteConfirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ListSkeleton() {
  return (
    <ul aria-hidden="true" className="divide-y border-b">
      {[0, 1, 2].map((i) => (
        <li key={i} className="space-y-2 px-4 py-4 md:px-8">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-3 w-56 max-w-full" />
        </li>
      ))}
    </ul>
  );
}
