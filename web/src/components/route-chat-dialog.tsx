import { Link, useNavigate } from "@tanstack/react-router";
import { BotIcon, SearchIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { chatTitle } from "@/components/message-item";
import { StatusDot } from "@/components/status-dot";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { useAutoFocus } from "@/hooks/use-auto-focus";
import { useAgents, useRouteChat } from "@/lib/api/queries";
import { type Agent, type Chat, LevelManage, LevelUse } from "@/lib/api/types.gen";
import { useT } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";

/*
 * "Route chat to agent" (UI-13): one click creates a private room with the chat and the agent
 * as members (POST /chats/{id}/route) and opens it. Rooms stay the only routing primitive; this
 * is the shortcut for the most common one.
 */

const usable = (a: Agent) => a.my_level === LevelManage || a.my_level === LevelUse;

export function RouteChatDialog({ chat, onClose }: { chat: Chat | null; onClose: () => void }) {
  const t = useT();
  return (
    <Dialog open={chat !== null} onOpenChange={(open) => (open ? null : onClose())}>
      <DialogContent className="flex max-h-[min(36rem,calc(100dvh-2rem))] flex-col gap-0 p-0 sm:max-w-md">
        <DialogHeader className="border-b px-5 pt-5 pb-4 text-left">
          <DialogTitle>{t("route.title", { chat: chat?.title ?? "" })}</DialogTitle>
          <DialogDescription>{t("route.body")}</DialogDescription>
        </DialogHeader>
        {chat ? <AgentChoice chat={chat} onClose={onClose} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function AgentChoice({ chat, onClose }: { chat: Chat; onClose: () => void }) {
  const t = useT();
  const autoFocus = useAutoFocus();
  const navigate = useNavigate();
  const agents = useAgents();
  const presence = useLiveStore((s) => s.presence);
  const route = useRouteChat();
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();
  const list = (agents.data ?? [])
    .filter(usable)
    .filter((a) => !q || a.name.toLowerCase().includes(q) || a.owner.username.includes(q));

  const pick = (agent: Agent) =>
    route.mutate(
      { chatId: chat.id, agentId: agent.id },
      {
        onSuccess: (room) => {
          toast.success(
            t("route.done", {
              chat: chatTitle(t, chat.title, chat.connector_kind),
              agent: agent.name,
            }),
          );
          onClose();
          void navigate({ to: "/rooms/$roomId", params: { roomId: room.id } });
        },
        onError: () => toast.error(t("route.failed")),
      },
    );

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
          placeholder={t("route.filter")}
          aria-label={t("route.filter")}
          className="pl-9"
          autoFocus={autoFocus}
        />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto py-2">
        {agents.isPending ? (
          <div className="space-y-3 px-5 py-3" aria-hidden="true">
            <Skeleton className="h-4 w-40" />
            <Skeleton className="h-4 w-52" />
          </div>
        ) : list.length === 0 ? (
          <div className="px-5 py-8 text-center text-sm text-muted-foreground">
            <p>{q ? t("picker.noMatch") : t("route.noAgents")}</p>
            {q ? null : (
              <Link
                to="/contacts"
                className="mt-3 inline-block font-medium text-foreground underline"
              >
                {t("picker.connectAgent")}
              </Link>
            )}
          </div>
        ) : (
          <ul aria-label={t("members.agents")}>
            {list.map((a) => (
              <li key={a.id}>
                <button
                  type="button"
                  disabled={route.isPending}
                  onClick={() => pick(a)}
                  className="flex w-full items-center gap-3 px-5 py-2.5 text-left outline-none transition-colors hover:bg-accent focus-visible:bg-accent disabled:opacity-60"
                >
                  <BotIcon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium">{a.name}</span>
                    <span className="flex items-center gap-2 text-xs text-muted-foreground">
                      <StatusDot online={presence[a.id]?.online ?? a.online} withLabel />
                      <span>@{a.owner.username}</span>
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </>
  );
}
