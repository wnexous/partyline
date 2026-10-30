import {
  KeyRoundIcon,
  PlayIcon,
  RefreshCwIcon,
  RotateCwIcon,
  ScrollTextIcon,
  SquareIcon,
  Trash2Icon,
} from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";
import {
  ContainerLogsTail,
  containerDesiredLabel,
  containerStateLabel,
  containerVariant,
  useContainerAction,
  useContainerAgent,
  useContainerLogs,
  useDeleteContainer,
} from "@/entities/container-agent";
import { errorText } from "@/shared/api/error-text";
import { useT } from "@/shared/i18n";
import { useIsMobile } from "@/shared/lib/device";
import { cn } from "@/shared/lib/utils";
import { StatusDot } from "@/shared/ui/atoms/status-dot";
import { ConfirmDialog } from "@/shared/ui/molecules/confirm-dialog";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/shared/ui/molecules/sheet";
import { Button } from "@/shared/ui/primitives/button";
import { Checkbox } from "@/shared/ui/primitives/checkbox";
import { Label } from "@/shared/ui/primitives/label";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { ContainerCredentialsDialog } from "./credentials-dialog";

/** The actions of the container, in the order the row lists them. */
const ACTIONS = [
  {
    action: "start",
    label: "container.start",
    pending: "container.starting",
    done: "container.started",
    icon: PlayIcon,
  },
  {
    action: "stop",
    label: "container.stop",
    pending: "container.stopping",
    done: "container.stopped",
    icon: SquareIcon,
  },
  {
    action: "restart",
    label: "container.restart",
    pending: "container.restarting",
    done: "container.restarted",
    icon: RotateCwIcon,
  },
] as const;

/**
 * The container of an AI (CA-06): the state the runner last reported and the one the owner wants,
 * the last error, start/stop/restart, the last log lines and deleting the container with or
 * without its workspace. It is one component for the AI's profile page and its panel, and it
 * renders nothing unless the viewer manages the AI and a container exists (the GET is 404
 * otherwise). The state reads again every ten seconds while it is on screen, since the runner
 * reports its containers over its own connection and no live event reaches the page.
 */
export function ContainerSection({
  agentId,
  name,
  manages,
  headingLevel: Heading = "h2",
  headingClassName,
  className,
  testId,
}: {
  agentId: string;
  /** The AI's display name, for the delete question. */
  name: string;
  /** Whether the viewer manages the AI: only then is the container read at all. */
  manages: boolean;
  headingLevel?: "h2" | "h3";
  headingClassName?: string;
  className?: string;
  testId?: string;
}) {
  const t = useT();
  const id = useId();
  const container = useContainerAgent(agentId, { enabled: manages, poll: manages });
  const run = useContainerAction(agentId);
  const [logsOpen, setLogsOpen] = useState(false);
  const [credentialsOpen, setCredentialsOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);

  if (!manages) return null;
  // No container (404) is the common case: the AI runs in a terminal. Nothing to show, nothing to
  // say; a failure other than 404 is silent too, so a profile never grows an error for a probe.
  if (!container.data) return null;
  const data = container.data;

  return (
    <section
      aria-labelledby={`${id}-container`}
      data-testid={testId}
      className={cn("space-y-3", className)}
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Heading
          id={`${id}-container`}
          className={headingClassName ?? "text-lg font-semibold tracking-[-0.015em]"}
        >
          {t("container.title")}
        </Heading>
        <StatusDot
          online={data.state === "running"}
          variant={containerVariant(data.state)}
          label={t(containerStateLabel(data.state))}
          withLabel
          labelClassName="text-sm text-foreground"
        />
      </div>

      <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[9rem_minmax(0,1fr)]">
        <dt className="text-muted-foreground">{t("container.desired")}</dt>
        <dd className="min-w-0 [overflow-wrap:anywhere]">
          {t(containerDesiredLabel(data.desired))}
        </dd>
        {data.last_error ? (
          <>
            <dt className="text-muted-foreground">{t("container.lastError")}</dt>
            <dd className="min-w-0 text-destructive [overflow-wrap:anywhere]">{data.last_error}</dd>
          </>
        ) : null}
      </dl>

      <div className="flex flex-wrap gap-2">
        {ACTIONS.map(({ action, label, pending, done, icon: Icon }) => {
          // One operation at a time: the three share the pending state of the container.
          const running = run.isPending && run.variables === action;
          return (
            <Button
              key={action}
              variant="outline"
              size="sm"
              disabled={run.isPending}
              onClick={() =>
                run.mutate(action, {
                  onSuccess: () => toast.success(t(done)),
                  onError: (err) => toast.error(errorText(t, err, "container")),
                })
              }
            >
              <Icon aria-hidden="true" className={cn(running && "animate-spin")} />
              {running ? t(pending) : t(label)}
            </Button>
          );
        })}
        <Button variant="outline" size="sm" onClick={() => setCredentialsOpen(true)}>
          <KeyRoundIcon aria-hidden="true" />
          {t("container.credentials")}
        </Button>
        <Button variant="outline" size="sm" onClick={() => setLogsOpen(true)}>
          <ScrollTextIcon aria-hidden="true" />
          {t("container.logs")}
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="text-destructive hover:text-destructive"
          onClick={() => setDeleting(true)}
        >
          <Trash2Icon aria-hidden="true" />
          {t("container.delete")}
        </Button>
      </div>

      {credentialsOpen ? (
        <ContainerCredentialsDialog
          agentId={agentId}
          harness={data.harness}
          env={data.env}
          onClose={() => setCredentialsOpen(false)}
        />
      ) : null}
      {logsOpen ? (
        <ContainerLogs
          agentId={agentId}
          name={data.container_name}
          onClose={() => setLogsOpen(false)}
        />
      ) : null}
      {deleting ? (
        <DeleteContainerDialog agentId={agentId} name={name} onClose={() => setDeleting(false)} />
      ) : null}
    </section>
  );
}

/** The last log lines: monospace, scrolling, wrapping at 360 px, with its own refresh. */
function ContainerLogs({
  agentId,
  name,
  onClose,
}: {
  agentId: string;
  name: string;
  onClose: () => void;
}) {
  const t = useT();
  const isMobile = useIsMobile();
  const logs = useContainerLogs(agentId, true);
  const text = logs.data?.logs ?? "";
  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <SheetContent
        side={isMobile ? "bottom" : "right"}
        className={cn("gap-0", isMobile && "max-h-[85dvh] rounded-t-lg")}
      >
        <SheetHeader className="gap-1 border-b px-4 py-3 pr-14">
          <SheetTitle>{t("container.logsTitle", { count: ContainerLogsTail })}</SheetTitle>
          <SheetDescription translate="no" className="font-mono text-xs">
            {t("container.logsBody", { name })}
          </SheetDescription>
        </SheetHeader>
        <div className="flex min-h-0 flex-1 flex-col gap-3 px-4 py-3">
          <div>
            <Button
              variant="outline"
              size="sm"
              disabled={logs.isFetching}
              onClick={() => void logs.refetch()}
            >
              <RefreshCwIcon aria-hidden="true" className={cn(logs.isFetching && "animate-spin")} />
              {t("container.logsRefresh")}
            </Button>
          </div>
          {logs.isPending ? (
            <Skeleton className="h-40 w-full" />
          ) : logs.isError ? (
            <p role="alert" className="text-sm text-destructive">
              {t("container.logsError")}
            </p>
          ) : text.trim() === "" ? (
            <p className="text-sm text-muted-foreground">{t("container.logsEmpty")}</p>
          ) : (
            <pre
              translate="no"
              className="min-h-0 flex-1 overflow-auto overscroll-contain rounded-md border bg-muted/45 p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap [overflow-wrap:anywhere]"
            >
              {text}
            </pre>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

/** Deletes the container, asking whether the workspace volume goes with it (CA-06). */
function DeleteContainerDialog({
  agentId,
  name,
  onClose,
}: {
  agentId: string;
  name: string;
  onClose: () => void;
}) {
  const t = useT();
  const id = useId();
  const remove = useDeleteContainer(agentId);
  const [volume, setVolume] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <ConfirmDialog
      open
      onClose={onClose}
      title={t("container.deleteTitle", { name })}
      body={t("container.deleteBody")}
      confirmLabel={t("container.deleteConfirm")}
      pending={remove.isPending}
      onConfirm={() => {
        setError(null);
        remove.mutate(
          { volume },
          {
            onSuccess: () => {
              toast(t("container.deleted"));
              onClose();
            },
            onError: (err) => setError(errorText(t, err, "container")),
          },
        );
      }}
    >
      <div className="flex items-start gap-2.5">
        <Checkbox
          id={`${id}-volume`}
          checked={volume}
          onCheckedChange={(checked) => setVolume(checked === true)}
          className="mt-0.5 size-5"
        />
        <div className="space-y-0.5">
          <Label htmlFor={`${id}-volume`} className="font-medium">
            {t("container.deleteVolume")}
          </Label>
          <p className="text-xs text-muted-foreground">{t("container.deleteVolumeHint")}</p>
        </div>
      </div>
      {error ? (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      ) : null}
    </ConfirmDialog>
  );
}
