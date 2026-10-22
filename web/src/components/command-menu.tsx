import { useNavigate } from "@tanstack/react-router";
import { BotIcon, MessagesSquareIcon, PlugZapIcon } from "lucide-react";
import { type ReactNode, useEffect } from "react";
import { NAV_GROUPS } from "@/components/app-sidebar";
import { ConnectorStatusBadge, useConnectorState } from "@/components/connector-status";
import { KindBadge } from "@/components/intelligence/kind-badge";
import { platformLabel } from "@/components/message-item";
import { StatusDot } from "@/components/status-dot";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { useAgents, useConnectors, useConversations } from "@/lib/api/queries";
import { type Connector, type Me, RoleAdmin } from "@/lib/api/types.gen";
import { useT } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";
import { cn } from "@/lib/utils";

/*
 * Command palette (UI-22): Ctrl/Cmd+K from anywhere in the app jumps to a conversation, an agent,
 * a connector or a page. The open flag lives in the live store so any trigger can open it.
 */

export function CommandMenu({ me }: { me: Me }) {
  const t = useT();
  const open = useLiveStore((s) => s.paletteOpen);
  const setOpen = useLiveStore((s) => s.setPaletteOpen);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() !== "k" || !(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey) {
        return;
      }
      e.preventDefault();
      setOpen(!useLiveStore.getState().paletteOpen);
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [setOpen]);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent
        showCloseButton={false}
        className="top-[12%] translate-y-0 gap-0 overflow-hidden p-0 sm:top-[18%] sm:max-w-xl"
      >
        <DialogTitle className="sr-only">{t("palette.title")}</DialogTitle>
        <DialogDescription className="sr-only">{t("palette.description")}</DialogDescription>
        {/* Mounted only while open: lists load on demand and the search starts empty. */}
        {open ? <PaletteBody me={me} onDone={() => setOpen(false)} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function PaletteBody({ me, onDone }: { me: Me; onDone: () => void }) {
  const t = useT();
  const navigate = useNavigate();
  const conversations = useConversations();
  const agents = useAgents();
  const connectors = useConnectors();
  const presence = useLiveStore((s) => s.presence);

  const go = (to: string, params?: Record<string, string>) => {
    onDone();
    // biome-ignore lint/suspicious/noExplicitAny: targets come from typed route constants above
    void navigate({ to, params } as any);
  };

  const pages = NAV_GROUPS.filter((g) => !g.adminOnly || me.role === RoleAdmin).flatMap(
    (g) => g.items,
  );

  return (
    <Command
      loop
      className="rounded-none bg-popover [&_[cmdk-group-heading]]:font-semibold [&_[cmdk-group-heading]]:tracking-[0.06em] [&_[cmdk-group-heading]]:uppercase [&_[cmdk-group-heading]]:[font-stretch:78%] [&_[cmdk-group-heading]]:px-2.5 [&_[cmdk-group-heading]]:pt-3 [&_[cmdk-group-heading]]:pb-1.5 [&_[cmdk-group-heading]]:text-[0.68rem] [&_[cmdk-group-heading]]:text-muted-foreground"
    >
      <CommandInput
        placeholder={t("palette.placeholder")}
        aria-label={t("palette.placeholder")}
        className="h-12 text-[0.9375rem]"
      />
      <CommandList className="max-h-[min(26rem,60dvh)] px-1.5 pb-1.5">
        <CommandEmpty className="py-10 text-center text-sm text-muted-foreground">
          {t("palette.empty")}
        </CommandEmpty>

        {conversations.data?.length ? (
          <CommandGroup heading={t("palette.rooms")}>
            {conversations.data.map((c) => (
              <PaletteItem
                key={c.id}
                value={`room:${c.id}`}
                keywords={[c.title, ...c.member_names]}
                onSelect={() => go("/rooms/$roomId", { roomId: c.id })}
                icon={<MessagesSquareIcon aria-hidden="true" />}
                label={c.title}
                hint={c.counterpart ? `@${c.counterpart.handle}` : undefined}
              />
            ))}
          </CommandGroup>
        ) : null}

        {agents.data?.length ? (
          <CommandGroup heading={t("palette.agents")}>
            {agents.data.map((agent) => (
              <PaletteItem
                key={agent.id}
                value={`agent:${agent.id}`}
                keywords={[
                  agent.intelligence.handle,
                  agent.intelligence.display_name,
                  agent.description,
                ]}
                onSelect={() => go("/contacts/$id", { id: agent.id })}
                icon={<BotIcon aria-hidden="true" />}
                label={agent.intelligence.display_name}
                badge={<KindBadge intelligence={agent.intelligence} />}
                hint={`@${agent.intelligence.handle}`}
                trailing={<StatusDot online={presence[agent.id]?.online ?? agent.online} />}
              />
            ))}
          </CommandGroup>
        ) : null}

        {connectors.data?.length ? (
          <CommandGroup heading={t("palette.connectors")}>
            {connectors.data.map((c) => (
              <ConnectorItem
                key={c.id}
                connector={c}
                onSelect={() => go("/connectors/$connectorId", { connectorId: c.id })}
              />
            ))}
          </CommandGroup>
        ) : null}

        <CommandGroup heading={t("palette.pages")}>
          {pages.map((page) => (
            <PaletteItem
              key={page.to}
              value={`page:${page.to}`}
              keywords={[t(page.label)]}
              onSelect={() => go(page.to)}
              icon={<page.icon aria-hidden="true" />}
              label={t(page.label)}
            />
          ))}
        </CommandGroup>
      </CommandList>
    </Command>
  );
}

function ConnectorItem({ connector: c, onSelect }: { connector: Connector; onSelect: () => void }) {
  const { status } = useConnectorState(c);
  return (
    <PaletteItem
      value={`connector:${c.id}`}
      keywords={[c.name, platformLabel(c.kind)]}
      onSelect={onSelect}
      icon={<PlugZapIcon aria-hidden="true" />}
      label={c.name}
      hint={platformLabel(c.kind)}
      trailing={<ConnectorStatusBadge status={status} enabled={c.enabled} />}
    />
  );
}

function PaletteItem({
  value,
  keywords,
  onSelect,
  icon,
  label,
  badge,
  hint,
  trailing,
}: {
  value: string;
  keywords: string[];
  onSelect: () => void;
  icon: ReactNode;
  label: string;
  /** Marks what the name belongs to (PROF-09), e.g. the AI badge. */
  badge?: ReactNode;
  hint?: string;
  trailing?: ReactNode;
}) {
  return (
    <CommandItem
      value={value}
      keywords={keywords.filter(Boolean)}
      onSelect={onSelect}
      className={cn(
        "gap-3 rounded-md px-2.5 py-2",
        "data-[selected=true]:bg-tape/55 data-[selected=true]:text-tape-foreground dark:data-[selected=true]:bg-tape/14 dark:data-[selected=true]:text-tape",
      )}
    >
      {icon}
      <span className="max-w-[70%] shrink-0 truncate font-medium">{label}</span>
      {badge}
      {hint ? <span className="min-w-0 truncate text-xs text-muted-foreground">{hint}</span> : null}
      {trailing ? <span className="ml-auto shrink-0">{trailing}</span> : null}
    </CommandItem>
  );
}
