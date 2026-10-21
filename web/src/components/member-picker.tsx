import { Link } from "@tanstack/react-router";
import { BotIcon, FolderIcon, MessageCircleIcon, PlusIcon, SearchIcon } from "lucide-react";
import { type ReactNode, useMemo, useState } from "react";
import { toast } from "sonner";
import { chatTitle, platformLabel } from "@/components/message-item";
import { StatusDot } from "@/components/status-dot";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { useAutoFocus } from "@/hooks/use-auto-focus";
import { useAddMember, useAgents, useChats, useProjects } from "@/lib/api/queries";
import {
  type Agent,
  AgentKindTemporary,
  type Chat,
  LevelManage,
  LevelUse,
  type MemberKind,
  type Project,
  type RoomMember,
} from "@/lib/api/types.gen";
import { useI18n } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";

/*
 * Adds projects, agents and chats to a room (UI-10, PROJ-06). Only what the user may use is
 * offered (ACL-05, ACL-06): projects and agents they own or hold `use` on, chats of connectors
 * they can use (GET /chats). Projects come first: a project member reaches whichever of its
 * sessions is open, so it outlives any one session; fixed agents come before temporary ones.
 */

const usableAgent = (a: Agent) => a.my_level === LevelManage || a.my_level === LevelUse;

function matches(query: string, ...fields: (string | undefined)[]) {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return fields.some((f) => f?.toLowerCase().includes(q));
}

export function MemberPicker({
  roomId,
  members,
  trigger,
}: {
  roomId: string;
  members: RoomMember[];
  trigger: ReactNode;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="flex max-h-[min(40rem,calc(100dvh-2rem))] flex-col gap-0 p-0 sm:max-w-lg">
        <DialogHeader className="border-b px-5 pt-5 pb-4 text-left">
          <DialogTitle>{t("picker.title")}</DialogTitle>
          <DialogDescription>{t("picker.body")}</DialogDescription>
        </DialogHeader>
        {open ? <PickerBody roomId={roomId} members={members} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function PickerBody({ roomId, members }: { roomId: string; members: RoomMember[] }) {
  const { t } = useI18n();
  const autoFocus = useAutoFocus();
  const agents = useAgents();
  const projects = useProjects();
  const chats = useChats();
  const add = useAddMember(roomId);
  const presence = useLiveStore((s) => s.presence);
  const [query, setQuery] = useState("");
  const [adding, setAdding] = useState<string | null>(null);

  const taken = useMemo(() => new Set(members.map((m) => `${m.kind}:${m.member_id}`)), [members]);
  const agentList = (agents.data ?? [])
    .filter(
      (a) =>
        usableAgent(a) &&
        !taken.has(`agent:${a.id}`) &&
        matches(query, a.display_name, a.name, a.description, a.owner.username, a.project?.name),
    )
    .sort(
      (a, b) =>
        Number(a.kind === AgentKindTemporary) - Number(b.kind === AgentKindTemporary) ||
        a.display_name.localeCompare(b.display_name),
    );
  const projectList = (projects.data ?? []).filter(
    (p: Project) =>
      !taken.has(`project:${p.id}`) && matches(query, p.name, p.slug, p.owner.username),
  );
  // Online sessions per project, from the agents the user can see.
  const onlineByProject = new Map<string, number>();
  for (const a of agents.data ?? []) {
    if (a.project && (presence[a.id]?.online ?? a.online)) {
      onlineByProject.set(a.project.id, (onlineByProject.get(a.project.id) ?? 0) + 1);
    }
  }
  const chatList = (chats.data ?? []).filter(
    (c) =>
      !taken.has(`chat:${c.id}`) &&
      matches(query, chatTitle(t, c.title, c.connector_kind), c.connector_name, c.connector_kind),
  );

  const onAdd = (kind: MemberKind, id: string, name: string) => {
    setAdding(`${kind}:${id}`);
    add.mutate(
      { kind, member_id: id },
      {
        onSuccess: () => toast.success(t("picker.added", { name })),
        onError: () => toast.error(t("picker.addFailed", { name })),
        onSettled: () => setAdding(null),
      },
    );
  };

  const loading = agents.isPending || chats.isPending || projects.isPending;
  const nothing =
    !loading && agentList.length === 0 && chatList.length === 0 && projectList.length === 0;

  return (
    <>
      <div className="relative border-b px-5 py-3">
        <SearchIcon
          className="pointer-events-none absolute top-1/2 left-8 size-4 -translate-y-1/2 text-muted-foreground"
          aria-hidden="true"
        />
        <Input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("picker.filterPlaceholder")}
          aria-label={t("picker.filter")}
          className="pl-9"
          autoFocus={autoFocus}
        />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto py-2">
        {loading ? (
          <div className="space-y-3 px-5 py-3" aria-hidden="true">
            <Skeleton className="h-4 w-40" />
            <Skeleton className="h-4 w-56" />
            <Skeleton className="h-4 w-48" />
          </div>
        ) : nothing ? (
          <div className="px-5 py-8 text-center text-sm text-muted-foreground">
            <p>{query ? t("picker.noMatch") : t("picker.nothing")}</p>
            {!query ? (
              <p className="mt-3 flex justify-center gap-4">
                <Link to="/agents" className="font-medium text-foreground underline">
                  {t("picker.connectAgent")}
                </Link>
                <Link to="/connectors" className="font-medium text-foreground underline">
                  {t("picker.addConnector")}
                </Link>
              </p>
            ) : null}
          </div>
        ) : (
          <>
            {projectList.length > 0 ? (
              <CandidateGroup label={t("members.projects")}>
                {projectList.map((p) => {
                  const name = t("picker.projectAny", { name: p.name });
                  const online = onlineByProject.get(p.id) ?? 0;
                  return (
                    <Candidate
                      key={p.id}
                      testId={`candidate-project-${p.id}`}
                      icon={
                        <FolderIcon className="size-4 text-muted-foreground" aria-hidden="true" />
                      }
                      name={name}
                      meta={
                        <>
                          <StatusDot online={online > 0} />
                          <span>
                            {online > 0
                              ? t("picker.projectOnline", { count: online })
                              : t("picker.noSession")}
                          </span>
                          <span>@{p.owner.username}</span>
                        </>
                      }
                      busy={adding === `project:${p.id}`}
                      onAdd={() => onAdd("project", p.id, name)}
                    />
                  );
                })}
              </CandidateGroup>
            ) : null}
            {agentList.length > 0 ? (
              <CandidateGroup label={t("members.agents")}>
                {agentList.map((a) => (
                  <Candidate
                    key={a.id}
                    testId={`candidate-agent-${a.id}`}
                    icon={<BotIcon className="size-4 text-muted-foreground" aria-hidden="true" />}
                    name={a.display_name}
                    meta={
                      <>
                        <StatusDot online={presence[a.id]?.online ?? a.online} withLabel />
                        <span>@{a.owner.username}</span>
                        {a.kind === AgentKindTemporary ? (
                          <span>{t("agents.temporary")}</span>
                        ) : null}
                      </>
                    }
                    busy={adding === `agent:${a.id}`}
                    onAdd={() => onAdd("agent", a.id, a.display_name)}
                  />
                ))}
              </CandidateGroup>
            ) : null}
            {chatList.length > 0 ? (
              <CandidateGroup label={t("members.chats")}>
                {chatList.map((c: Chat) => (
                  <Candidate
                    key={c.id}
                    testId={`candidate-chat-${c.id}`}
                    icon={<MessageCircleIcon className="size-4 text-info" aria-hidden="true" />}
                    name={chatTitle(t, c.title, c.connector_kind)}
                    meta={
                      <span>
                        {platformLabel(c.connector_kind)} · {c.connector_name}
                      </span>
                    }
                    busy={adding === `chat:${c.id}`}
                    onAdd={() => onAdd("chat", c.id, chatTitle(t, c.title, c.connector_kind))}
                  />
                ))}
              </CandidateGroup>
            ) : null}
          </>
        )}
      </div>
    </>
  );
}

function CandidateGroup({ label, children }: { label: string; children: ReactNode }) {
  return (
    <section aria-label={label} className="py-1">
      <h3 className="px-5 pt-2 pb-1 label-caps text-[0.68rem] text-muted-foreground">{label}</h3>
      <ul>{children}</ul>
    </section>
  );
}

function Candidate({
  testId,
  icon,
  name,
  meta,
  busy,
  onAdd,
}: {
  testId: string;
  icon: ReactNode;
  name: string;
  meta: ReactNode;
  busy: boolean;
  onAdd: () => void;
}) {
  const { t } = useI18n();
  return (
    <li data-testid={testId} className="flex items-center gap-3 px-5 py-2 hover:bg-accent/40">
      <span className="shrink-0">{icon}</span>
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium">{name}</p>
        <p className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">{meta}</p>
      </div>
      <Button
        size="sm"
        variant="outline"
        disabled={busy}
        aria-label={t("picker.add", { name })}
        onClick={onAdd}
      >
        <PlusIcon aria-hidden="true" />
        {t("picker.addShort")}
      </Button>
    </li>
  );
}
