import { createFileRoute } from "@tanstack/react-router";
import {
  ArchiveIcon,
  BotIcon,
  CheckIcon,
  FolderIcon,
  PencilIcon,
  PinIcon,
  PlugIcon,
  Share2Icon,
  Trash2Icon,
  XIcon,
} from "lucide-react";
import { type FormEvent, type ReactNode, useId, useMemo, useState } from "react";
import { toast } from "sonner";
import { ConnectAgentDialog } from "@/components/connect-agent-dialog";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { SharingDialog } from "@/components/sharing-dialog";
import { StatusDot } from "@/components/status-dot";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { type AgentGroup, distinct, groupByProject, harnessLabel } from "@/lib/agents";
import {
  useAgentsWithArchived,
  useDeleteAgent,
  useMe,
  useRenameProject,
  useUpdateAgent,
} from "@/lib/api/queries";
import {
  type Agent,
  AgentKindTemporary,
  LevelManage,
  MaxAgentDisplayName,
  MaxProjectName,
} from "@/lib/api/types.gen";
import { useI18n } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";
import { type AgentView, withPresence } from "@/lib/presence";
import { formatAbsolute, formatRelative } from "@/lib/time";

export const Route = createFileRoute("/_app/agents")({
  component: AgentsPage,
});

const ALL = "all";

/*
 * Agents page (UI-15, PROJ-04): agents grouped by project, projects with a live session first,
 * filtered by machine and harness, archived agents behind a toggle. Owners rename agents and
 * projects in place and keep a temporary agent (make it fixed).
 */
function AgentsPage() {
  const { t } = useI18n();
  const agents = useAgentsWithArchived();
  const presence = useLiveStore((s) => s.presence);
  const [deleting, setDeleting] = useState<Agent | null>(null);
  const [machine, setMachine] = useState(ALL);
  const [harness, setHarness] = useState(ALL);
  const [showArchived, setShowArchived] = useState(false);

  const views = useMemo(
    () => (agents.data ?? []).map((a) => withPresence(a, presence[a.id])),
    [agents.data, presence],
  );
  const archivedCount = views.filter((a) => a.archived).length;
  const visible = views.filter((a) => showArchived || !a.archived);
  const machines = distinct(visible, (a) => a.machine);
  const harnesses = distinct(visible, (a) => a.harness);
  const groups = groupByProject(
    visible.filter(
      (a) =>
        (machine === ALL || a.machine === machine) && (harness === ALL || a.harness === harness),
    ),
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
        actions={views.length > 0 ? connect : null}
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
      ) : views.length === 0 ? (
        <EmptyState icon={BotIcon} title={t("agents.emptyTitle")} action={connect}>
          <p>{t("agents.emptyBody")}</p>
        </EmptyState>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2 border-b px-4 py-2.5 md:px-8">
            <FilterSelect
              label={t("agents.filterMachine")}
              allLabel={t("agents.allMachines")}
              value={machine}
              options={machines.map((m) => ({ value: m, label: m }))}
              onChange={setMachine}
              mono
            />
            <FilterSelect
              label={t("agents.filterHarness")}
              allLabel={t("agents.allHarnesses")}
              value={harness}
              options={harnesses.map((h) => ({
                value: h,
                label: harnessLabel(h) ?? t("agents.harnessUnknown"),
              }))}
              onChange={setHarness}
            />
            {archivedCount > 0 ? (
              <ArchivedToggle
                checked={showArchived}
                onChange={setShowArchived}
                count={archivedCount}
              />
            ) : null}
          </div>
          {groups.length === 0 ? (
            <div className="px-4 py-10 text-sm text-muted-foreground md:px-8">
              <p>{t("agents.noMatch")}</p>
              <Button
                variant="outline"
                size="sm"
                className="mt-3"
                onClick={() => {
                  setMachine(ALL);
                  setHarness(ALL);
                }}
              >
                {t("agents.clearFilters")}
              </Button>
            </div>
          ) : (
            <div className="pb-10">
              {groups.map((g) => (
                <ProjectGroup key={g.key} group={g} onDelete={setDeleting} />
              ))}
            </div>
          )}
        </>
      )}
      <ConnectAgentDialog open={connecting} onOpenChange={setConnecting} />
      <DeleteAgentDialog agent={deleting} onClose={() => setDeleting(null)} />
    </>
  );
}

function FilterSelect({
  label,
  allLabel,
  value,
  options,
  onChange,
  mono = false,
}: {
  label: string;
  allLabel: string;
  value: string;
  options: { value: string; label: string }[];
  onChange: (v: string) => void;
  mono?: boolean;
}) {
  if (options.length < 2 && value === ALL) return null;
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger size="sm" aria-label={label} className="min-w-36">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={ALL}>{allLabel}</SelectItem>
        {options.map((o) => (
          <SelectItem
            key={o.value}
            value={o.value}
            className={mono ? "font-mono text-xs" : undefined}
          >
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function ArchivedToggle({
  checked,
  onChange,
  count,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  count: number;
}) {
  const { t } = useI18n();
  const id = useId();
  return (
    <div className="ml-auto flex items-center gap-2 text-sm text-muted-foreground">
      <Switch id={id} size="sm" checked={checked} onCheckedChange={onChange} />
      <label htmlFor={id}>{t("agents.showArchived", { count })}</label>
    </div>
  );
}

function ProjectGroup({ group, onDelete }: { group: AgentGroup; onDelete: (a: Agent) => void }) {
  const { t } = useI18n();
  const me = useMe().data;
  const headingId = useId();
  const [renaming, setRenaming] = useState(false);
  const rename = useRenameProject();
  const project = group.project;
  const name = project?.name ?? t("agents.noProject");
  // Projects belong to their agents' owner: whoever manages one of its agents owns it.
  const canRename =
    project !== undefined &&
    group.agents.some((a) => a.my_level === LevelManage && (!me || a.owner.id === me.id));

  return (
    <section aria-labelledby={headingId}>
      <div className="flex min-h-12 items-center gap-3 px-4 pt-5 pb-2 md:px-8">
        {project ? (
          <FolderIcon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
        ) : null}
        {renaming && project ? (
          <InlineRename
            label={t("agents.renameProject", { name })}
            initial={project.name}
            max={MaxProjectName}
            pending={rename.isPending}
            onCancel={() => setRenaming(false)}
            onSave={(next) =>
              rename.mutate(
                { id: project.id, name: next },
                {
                  onSuccess: () => {
                    toast(t("agents.projectRenamed", { name: next }));
                    setRenaming(false);
                  },
                  onError: () => toast.error(t("agents.projectRenameFailed", { name })),
                },
              )
            }
          />
        ) : (
          <>
            <h2
              id={headingId}
              className={
                project
                  ? "min-w-0 truncate text-base font-semibold tracking-[-0.01em]"
                  : "label-caps text-[0.72rem] text-muted-foreground"
              }
            >
              {name}
            </h2>
            <span className="inline-flex shrink-0 items-center gap-1.5 font-mono text-xs text-muted-foreground tabular">
              {group.online > 0 ? (
                <span aria-hidden="true" className="size-1.5 rounded-full bg-tape-strong" />
              ) : null}
              {t("agents.groupOnline", { online: group.online, total: group.agents.length })}
            </span>
            {canRename ? (
              <Button
                variant="ghost"
                size="icon-xs"
                className="text-muted-foreground"
                aria-label={t("agents.renameProject", { name })}
                title={t("agents.renameShort")}
                onClick={() => setRenaming(true)}
              >
                <PencilIcon aria-hidden="true" />
              </Button>
            ) : null}
          </>
        )}
      </div>
      <ul aria-label={t("agents.groupLabel", { name })} className="divide-y border-y">
        {group.agents.map((agent) => (
          <AgentRow key={agent.id} agent={agent} onDelete={() => onDelete(agent)} />
        ))}
      </ul>
    </section>
  );
}

function AgentRow({ agent, onDelete }: { agent: AgentView; onDelete: () => void }) {
  const { t, locale } = useI18n();
  const me = useMe().data;
  const update = useUpdateAgent();
  const [renaming, setRenaming] = useState(false);
  const foreign = me && agent.owner.id !== me.id;
  const canManage = agent.my_level === LevelManage;
  const name = agent.display_name;
  const temporary = agent.kind === AgentKindTemporary;

  const keep = () =>
    update.mutate(
      { id: agent.id, fixed: true },
      {
        onSuccess: () => toast(t("agents.kept", { name })),
        onError: () => toast.error(t("agents.keepFailed", { name })),
      },
    );

  return (
    <li
      aria-label={name}
      className="grid grid-cols-1 items-center gap-x-4 gap-y-1.5 px-4 py-3.5 sm:grid-cols-[minmax(0,1fr)_auto] md:px-8"
    >
      <div className="min-w-0">
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <BotIcon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          {renaming ? (
            <InlineRename
              label={t("agents.renameLabel", { name })}
              initial={name}
              max={MaxAgentDisplayName}
              pending={update.isPending}
              onCancel={() => setRenaming(false)}
              onSave={(next) =>
                update.mutate(
                  { id: agent.id, display_name: next },
                  {
                    onSuccess: () => {
                      toast(t("agents.renamed", { name: next }));
                      setRenaming(false);
                    },
                    onError: () => toast.error(t("agents.renameFailed", { name })),
                  },
                )
              }
            />
          ) : (
            <span className="min-w-0 font-semibold tracking-[-0.01em] break-words sm:truncate">
              {name}
            </span>
          )}
          {foreign ? (
            <span className="shrink-0 text-xs text-muted-foreground">
              {t("rooms.ownedBy", { username: agent.owner.username })}
            </span>
          ) : null}
          {temporary ? <Tag title={t("agents.temporaryHint")}>{t("agents.temporary")}</Tag> : null}
          {agent.archived ? (
            <Tag>
              <ArchiveIcon className="size-3" aria-hidden="true" />
              {t("agents.archived")}
            </Tag>
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
          <span className="text-xs">
            {harnessLabel(agent.harness) ?? t("agents.harnessUnknown")}
          </span>
          {agent.machine ? <span className="font-mono text-xs">{agent.machine}</span> : null}
          {agent.name !== name ? (
            <span className="min-w-0 truncate font-mono text-xs">{agent.name}</span>
          ) : null}
          {agent.description ? <span className="min-w-0 truncate">{agent.description}</span> : null}
        </div>
      </div>
      {canManage && !renaming ? (
        <div className="-ml-2 flex items-center gap-1 pl-6 sm:ml-0 sm:pl-0">
          {temporary ? (
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={t("agents.keep", { name })}
              title={t("agents.keepHint")}
              disabled={update.isPending}
              onClick={keep}
            >
              <PinIcon aria-hidden="true" />
            </Button>
          ) : null}
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={t("agents.rename", { name })}
            title={t("agents.renameShort")}
            onClick={() => setRenaming(true)}
          >
            <PencilIcon aria-hidden="true" />
          </Button>
          <SharingDialog
            kind="agents"
            id={agent.id}
            name={name}
            owner={agent.owner}
            trigger={
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={t("agents.share", { name })}
                title={t("room.share")}
              >
                <Share2Icon aria-hidden="true" />
              </Button>
            }
          />
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={t("agents.delete", { name })}
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

/** A small uppercase marker next to a name (kind, archived). */
function Tag({ children, title }: { children: ReactNode; title?: string }) {
  return (
    <span
      title={title}
      className="inline-flex shrink-0 items-center gap-1 rounded-sm border border-border px-1.5 py-px label-caps text-[0.62rem] text-muted-foreground"
    >
      {children}
    </span>
  );
}

/**
 * Renames in place: Enter saves, Escape cancels. The name is trimmed and must have 1 to max
 * characters (the server's rule, ID-04 and PROJ-05).
 */
function InlineRename({
  label,
  initial,
  max,
  pending,
  onSave,
  onCancel,
}: {
  label: string;
  initial: string;
  max: number;
  pending: boolean;
  onSave: (name: string) => void;
  onCancel: () => void;
}) {
  const { t } = useI18n();
  const [value, setValue] = useState(initial);
  const errorId = useId();
  const trimmed = value.trim();
  const length = [...trimmed].length;
  const invalid = length < 1 || length > max;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (invalid || pending) return;
    if (trimmed === initial) return onCancel();
    onSave(trimmed);
  };
  return (
    <form onSubmit={submit} className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
      <Input
        autoFocus
        aria-label={label}
        aria-invalid={invalid}
        aria-describedby={invalid ? errorId : undefined}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            e.preventDefault();
            onCancel();
          }
        }}
        className="h-8 w-full max-w-72 min-w-0 font-semibold"
      />
      <Button
        type="submit"
        size="icon-sm"
        aria-label={t("agents.save")}
        disabled={invalid || pending}
      >
        <CheckIcon aria-hidden="true" />
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        aria-label={t("common.cancel")}
        onClick={onCancel}
      >
        <XIcon aria-hidden="true" />
      </Button>
      {invalid ? (
        <p id={errorId} className="basis-full text-xs text-destructive">
          {t("agents.nameRule")}
        </p>
      ) : null}
    </form>
  );
}

function DeleteAgentDialog({ agent, onClose }: { agent: Agent | null; onClose: () => void }) {
  const { t } = useI18n();
  const remove = useDeleteAgent();
  const name = agent?.display_name ?? "";
  return (
    <AlertDialog open={agent !== null} onOpenChange={(open) => (open ? null : onClose())}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("agents.deleteTitle", { name })}</AlertDialogTitle>
          <AlertDialogDescription>{t("agents.deleteBody")}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction
            className="bg-destructive text-white hover:bg-destructive/90"
            disabled={remove.isPending}
            onClick={(e) => {
              e.preventDefault();
              if (!agent) return;
              remove.mutate(agent.id, {
                onSuccess: () => {
                  toast(t("agents.deleted", { name }));
                  onClose();
                },
                onError: () => toast.error(t("agents.deleteFailed", { name })),
              });
            }}
          >
            {t("agents.deleteConfirm")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
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
