import { zodResolver } from "@hookform/resolvers/zod";
import { ArrowUpRightIcon, KeyRoundIcon, TriangleAlertIcon } from "lucide-react";
import { type ReactNode, useState } from "react";
import { useForm } from "react-hook-form";
import { CodeBlock } from "@/components/copy-field";
import { FormAlert, TextField } from "@/components/form-fields";
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
import { Form } from "@/components/ui/form";
import {
  agentSnippets,
  CLAUDE_CODE_DOCS_URL,
  GO_INSTALL_COMMAND,
  RELEASES_URL,
} from "@/lib/agent-connect";
import { isApiError } from "@/lib/api/client";
import { useAgents, useCreateToken, useMe } from "@/lib/api/queries";
import { TokenKindAgent } from "@/lib/api/types.gen";
import { applyFieldErrors } from "@/lib/forms";
import { useT } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";
import { type ConnectAgentForm, connectAgentSchema } from "@/lib/schemas";
import { cn } from "@/lib/utils";

/*
 * "Connect an agent" (UI-14): pick the name the session will register as, create an agent token,
 * then copy the .mcp.json entry and the launch command. The token appears only here, once. The
 * agent registers on its first connection, so the last step waits for it to come online.
 */

interface Issued {
  name: string;
  secret: string;
}

/**
 * Controlled by the page: the wizard must survive the page switching from its empty state to the
 * list when the new agent registers, so its triggers live elsewhere.
 */
export function ConnectAgentDialog({
  open,
  onOpenChange: setOpen,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const t = useT();
  const [issued, setIssued] = useState<Issued | null>(null);

  const onOpenChange = (next: boolean) => {
    setOpen(next);
    if (!next) setIssued(null);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className={cn(
          "flex max-h-[calc(100dvh-2rem)] flex-col gap-0 p-0",
          issued ? "sm:max-w-2xl" : "sm:max-w-md",
        )}
        // The token is on screen: an outside click must not throw it away.
        onInteractOutside={issued ? (e) => e.preventDefault() : undefined}
      >
        <DialogHeader className="border-b px-5 pt-5 pb-4 text-left">
          <DialogTitle>{t("connect.title")}</DialogTitle>
          <DialogDescription>
            {issued ? t("connect.doneBody", { name: issued.name }) : t("connect.body")}
          </DialogDescription>
        </DialogHeader>
        {open ? (
          issued ? (
            <SnippetStep issued={issued} onDone={() => onOpenChange(false)} />
          ) : (
            <NameStep onIssued={setIssued} onCancel={() => onOpenChange(false)} />
          )
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function NameStep({
  onIssued,
  onCancel,
}: {
  onIssued: (issued: Issued) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const create = useCreateToken();
  const [formError, setFormError] = useState<string | null>(null);
  const form = useForm<ConnectAgentForm>({
    resolver: zodResolver(connectAgentSchema),
    defaultValues: { name: "" },
  });

  const onSubmit = form.handleSubmit((values) => {
    setFormError(null);
    const { name } = connectAgentSchema.parse(values);
    create.mutate(
      { kind: TokenKindAgent, name },
      {
        onSuccess: (created) => onIssued({ name, secret: created.secret }),
        onError: (err) => {
          if (applyFieldErrors(err, form.setError, ["name"])) return;
          setFormError(
            isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic"),
          );
        },
      },
    );
  });

  return (
    <Form {...form}>
      <form onSubmit={onSubmit} noValidate className="space-y-5 px-5 pt-4 pb-5">
        <FormAlert>{formError}</FormAlert>
        <TextField
          control={form.control}
          name="name"
          label={t("connect.name")}
          description={t("connect.nameHint")}
          autoComplete="off"
          autoFocus
          inputClassName="font-mono"
        />
        <p className="flex gap-2 text-xs text-muted-foreground">
          <KeyRoundIcon className="mt-px size-3.5 shrink-0" aria-hidden="true" />
          {t("connect.tokenNote")}
        </p>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onCancel}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" disabled={create.isPending}>
            {create.isPending ? t("connect.creating") : t("connect.create")}
          </Button>
        </DialogFooter>
      </form>
    </Form>
  );
}

function SnippetStep({ issued, onDone }: { issued: Issued; onDone: () => void }) {
  const t = useT();
  const { mcpJson, launch } = agentSnippets({
    origin: window.location.origin,
    token: issued.secret,
    name: issued.name,
  });

  return (
    <>
      <div className="min-h-0 flex-1 overflow-y-auto px-5 pt-4 pb-5">
        <div
          role="note"
          className="mb-5 flex gap-2.5 rounded-md bg-warning/15 px-3 py-2.5 text-sm ring-1 ring-warning/45 ring-inset"
        >
          <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          <p>{t("connect.onceWarning")}</p>
        </div>

        <ol className="space-y-6 [counter-reset:step]">
          <Step title={t("connect.stepInstall")}>
            <p>
              {t("connect.installBody")}{" "}
              <ExternalLink href={RELEASES_URL}>{t("connect.releases")}</ExternalLink>
            </p>
            <CodeBlock label={t("connect.installLabel")} value={GO_INSTALL_COMMAND} />
          </Step>
          <Step title={t("connect.stepConfig")}>
            <p>{t("connect.configBody")}</p>
            <CodeBlock label=".mcp.json" value={mcpJson} />
          </Step>
          <Step title={t("connect.stepLaunch")}>
            <p>{t("connect.launchBody")}</p>
            <CodeBlock label={t("connect.launchLabel")} value={launch} />
            <p className="text-xs">
              {t("connect.confirmNote")}{" "}
              <ExternalLink href={CLAUDE_CODE_DOCS_URL}>{t("connect.docs")}</ExternalLink>
            </p>
          </Step>
        </ol>
      </div>
      <DialogFooter className="flex-row items-center justify-between gap-3 border-t px-5 py-3.5">
        <AgentArrival name={issued.name} />
        <Button type="button" onClick={onDone}>
          {t("connect.done")}
        </Button>
      </DialogFooter>
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

/** Live line at the bottom of the wizard: waiting → online once the agent registers. */
function AgentArrival({ name }: { name: string }) {
  const t = useT();
  const me = useMe().data;
  const agents = useAgents();
  const presence = useLiveStore((s) => s.presence);
  const agent = agents.data?.find((a) => a.name === name && a.owner.id === me?.id);
  const online = agent ? (presence[agent.id]?.online ?? agent.online) : false;

  return (
    <p
      role="status"
      className={cn(
        "flex min-w-0 items-center gap-2 text-sm",
        online ? "font-medium text-foreground" : "text-muted-foreground",
      )}
    >
      <StatusDot online={online} />
      <span className="truncate">
        {online ? t("connect.online", { name }) : t("connect.waiting", { name })}
      </span>
    </p>
  );
}
