import { Link } from "@tanstack/react-router";
import { ArrowUpRightIcon, ChevronRightIcon, HourglassIcon, RadioTowerIcon } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { CodeBlock } from "@/components/copy-field";
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  CLAUDE_CODE_DOCS_URL,
  CONFIG_FILE,
  HARNESSES,
  type Harness,
  type HarnessId,
  installCommand,
  RELEASES_URL,
  SETUP_COMMAND,
} from "@/lib/agent-connect";
import { useAgents, useMe } from "@/lib/api/queries";
import { useT } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";
import { cn } from "@/lib/utils";

/*
 * "Connect an agent" (WIZ-01): install partyline from this instance, approve the machine once
 * with `partyline setup --browser` (it saves the credentials and registers every harness it
 * finds), then start a session of the chosen harness in the folder the AI works in. No token is
 * created or shown here; "Advanced" shows the manual entry, which reads the token from the config
 * file.
 * The footer waits for the first session that comes online while the dialog is open.
 */

/**
 * Controlled by the page: the wizard must survive the page switching from its empty state to the
 * list when the new agent registers, so its triggers live elsewhere.
 */
export function ConnectAgentDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const t = useT();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 p-0 sm:max-w-2xl">
        <DialogHeader className="border-b px-5 pt-5 pb-4 text-left">
          <DialogTitle>{t("connect.title")}</DialogTitle>
          <DialogDescription>{t("connect.body")}</DialogDescription>
        </DialogHeader>
        {open ? <Wizard onDone={() => onOpenChange(false)} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function Wizard({ onDone }: { onDone: () => void }) {
  const t = useT();
  const [harnessId, setHarnessId] = useState<HarnessId>("claude");
  const harness = HARNESSES.find((h) => h.id === harnessId) ?? (HARNESSES[0] as Harness);

  return (
    <>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-5 pt-4 pb-5">
        <ol className="space-y-6 [counter-reset:step]">
          <Step title={t("connect.stepInstall")}>
            <CodeBlock
              label={t("connect.installLabel")}
              value={installCommand(window.location.origin)}
            />
            <p className="text-xs">
              {t("connect.installBody")}{" "}
              <ExternalLink href={RELEASES_URL}>{t("connect.releases")}</ExternalLink>
            </p>
          </Step>
          <Step title={t("connect.stepSetup")}>
            <p>{t("connect.setupBody")}</p>
            <CodeBlock label={t("connect.setupLabel")} value={SETUP_COMMAND} />
          </Step>
          <Step title={t("connect.stepStart")}>
            <p>{t("connect.startBody")}</p>
            <Tabs value={harnessId} onValueChange={(v) => setHarnessId(v as HarnessId)}>
              <TabsList
                aria-label={t("connect.harness")}
                className="w-full flex-wrap justify-start gap-1 bg-transparent p-0 group-data-[orientation=horizontal]/tabs:h-auto"
              >
                {HARNESSES.map((h) => (
                  <TabsTrigger
                    key={h.id}
                    value={h.id}
                    className={cn(
                      "h-7 flex-none rounded-[3px] border-border px-2.5 text-[0.8rem]",
                      "data-[state=active]:border-tape-strong data-[state=active]:bg-tape data-[state=active]:text-tape-foreground data-[state=active]:shadow-none",
                      "dark:data-[state=active]:border-tape/50 dark:data-[state=active]:bg-tape/20 dark:data-[state=active]:text-tape",
                    )}
                  >
                    {h.title}
                  </TabsTrigger>
                ))}
              </TabsList>
              {HARNESSES.map((h) => (
                <TabsContent key={h.id} value={h.id} className="space-y-2.5 pt-1.5">
                  <CodeBlock label={t("connect.startLabel")} value={h.start} />
                  <Expectation harness={h} />
                </TabsContent>
              ))}
            </Tabs>
          </Step>
        </ol>
        <Advanced harness={harness} />
      </div>
      <DialogFooter className="flex-row items-center justify-between gap-3 border-t px-5 py-3.5">
        <AgentArrival />
        <Button type="button" onClick={onDone}>
          {t("connect.done")}
        </Button>
      </DialogFooter>
    </>
  );
}

/** One line on how room messages reach a session of this harness. */
function Expectation({ harness }: { harness: Harness }) {
  const t = useT();
  const push = harness.delivery === "push";
  const Icon = push ? RadioTowerIcon : HourglassIcon;
  return (
    <div className="space-y-1.5 text-xs">
      <p className="flex gap-2">
        <Icon className="mt-px size-3.5 shrink-0 text-foreground" aria-hidden="true" />
        <span>{push ? t("connect.push") : t("connect.turnEnd")}</span>
      </p>
      {harness.id === "claude" ? (
        <p>
          {t("connect.confirmNote")}{" "}
          <ExternalLink href={CLAUDE_CODE_DOCS_URL}>{t("connect.docs")}</ExternalLink>
        </p>
      ) : null}
    </div>
  );
}

/** Manual registration for the selected harness; the token stays in the config file. */
function Advanced({ harness }: { harness: Harness }) {
  const t = useT();
  return (
    <details className="group mt-6 border-t pt-4 text-sm text-muted-foreground">
      <summary className="flex w-fit cursor-pointer list-none items-center gap-1.5 rounded-sm font-medium text-foreground outline-none select-none focus-visible:ring-[3px] focus-visible:ring-ring/50 [&::-webkit-details-marker]:hidden">
        <ChevronRightIcon
          className="size-4 transition-transform duration-150 ease-out group-open:rotate-90 motion-reduce:transition-none"
          aria-hidden="true"
        />
        {t("connect.advanced")}
      </summary>
      <div className="mt-3 space-y-2.5 pl-5.5">
        <p>
          {t("connect.advancedBody", { harness: harness.title })}{" "}
          <code translate="no" className="font-mono text-[0.78rem] text-foreground">
            {CONFIG_FILE}
          </code>
        </p>
        <CodeBlock label={harness.manual.file} value={harness.manual.entry} />
        <p className="text-xs">
          {harness.id === "claude"
            ? t("connect.advancedClaude")
            : harness.delivery === "push"
              ? t("connect.advancedPush")
              : t("connect.advancedTurn")}
        </p>
        <p className="text-xs">
          <WithCommand text={t("connect.advancedToken")} command="partyline setup --token" />{" "}
          <Link
            to="/tokens"
            className="font-medium text-foreground underline underline-offset-3 hover:decoration-2"
          >
            {t("nav.tokens")}
          </Link>
        </p>
      </div>
    </details>
  );
}

/** Renders `{cmd}` in a sentence as an unbreakable inline command. */
function WithCommand({ text, command }: { text: string; command: string }) {
  const [before, after = ""] = text.split("{cmd}");
  return (
    <>
      {before}
      <code translate="no" className="font-mono whitespace-nowrap text-foreground">
        {command}
      </code>
      {after}
    </>
  );
}

function Step({ title, children }: { title: string; children: ReactNode }) {
  return (
    <li className="grid grid-cols-[1.5rem_minmax(0,1fr)] gap-x-3 [counter-increment:step]">
      <span
        aria-hidden="true"
        className="flex size-6 items-center justify-center rounded-[3px] bg-tape font-mono text-xs font-semibold text-tape-foreground before:content-[counter(step)] dark:bg-tape/20 dark:text-tape"
      />
      <div className="min-w-0 space-y-2.5 text-sm text-muted-foreground">
        <h3 className="pt-0.5 font-semibold text-foreground">{title}</h3>
        {children}
      </div>
    </li>
  );
}

function ExternalLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex items-center gap-0.5 font-medium text-foreground underline underline-offset-3 hover:decoration-2"
    >
      {children}
      <ArrowUpRightIcon className="size-3.5" aria-hidden="true" />
    </a>
  );
}

/**
 * Live line at the bottom of the wizard: the AI is the folder the session runs in, so it waits
 * for any of the viewer's agents that was not online when the dialog opened.
 */
function AgentArrival() {
  const t = useT();
  const me = useMe().data;
  const agents = useAgents().data;
  const presence = useLiveStore((s) => s.presence);
  const [before, setBefore] = useState<ReadonlySet<string> | null>(null);

  const mine = me && agents ? agents.filter((a) => a.owner.id === me.id) : undefined;
  const online = (id: string, fallback: boolean) => presence[id]?.online ?? fallback;

  useEffect(() => {
    if (before || !mine) return;
    setBefore(new Set(mine.filter((a) => online(a.id, a.online)).map((a) => a.id)));
  });

  const arrived = before
    ? mine?.find((a) => !before.has(a.id) && online(a.id, a.online))
    : undefined;

  return (
    <p
      role="status"
      className={cn(
        "flex min-w-0 items-center gap-2 text-sm",
        arrived ? "font-medium text-foreground" : "text-muted-foreground",
      )}
    >
      <StatusDot online={Boolean(arrived)} />
      <span className="truncate">
        {arrived ? t("connect.online", { name: arrived.name }) : t("connect.waiting")}
      </span>
    </p>
  );
}
