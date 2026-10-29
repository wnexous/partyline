import { PencilIcon, PlusIcon, ServerIcon, Trash2Icon } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";
import { RunnerRow, useDeleteRunner, useRenameRunner, useRunners } from "@/entities/runner";
import { errorText } from "@/shared/api/error-text";
import type { Runner } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { ConfirmDialog } from "@/shared/ui/molecules/confirm-dialog";
import { CodeBlock } from "@/shared/ui/molecules/copy-field";
import { EmptyState } from "@/shared/ui/molecules/empty-state";
import { InlineRename } from "@/shared/ui/molecules/inline-rename";
import { PageSection } from "@/shared/ui/molecules/page-section";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { RUNNER_CHECK_COMMAND, RUNNER_INSTALL_COMMAND, RUNNER_RUN_COMMAND } from "../lib/commands";

/**
 * "Servers" (CA-01, CA-02): the machines that run container AIs, with their online state, Docker
 * and container count, a way to rename or remove each one, and the two commands that add a new
 * one. The list reads again every ten seconds while it is open, because a server connects and
 * drops without an event of its own.
 */
export function ServersSection() {
  const t = useT();
  const id = useId();
  const runners = useRunners(true);
  const rename = useRenameRunner();
  const remove = useDeleteRunner();
  const [renaming, setRenaming] = useState<Runner | null>(null);
  const [removing, setRemoving] = useState<Runner | null>(null);
  const [renameError, setRenameError] = useState<string | null>(null);
  const [removeError, setRemoveError] = useState<string | null>(null);
  const list = runners.data ?? [];

  const startRename = (runner: Runner) => {
    setRenameError(null);
    setRenaming(runner);
  };
  const saveName = (runner: Runner, name: string) =>
    rename.mutate(
      { id: runner.id, name },
      {
        onSuccess: (updated) => {
          setRenaming(null);
          toast(t("servers.renamed", { name: updated.name }));
        },
        onError: (err) => setRenameError(errorText(t, err, "runner")),
      },
    );
  const startRemove = (runner: Runner) => {
    setRemoveError(null);
    setRemoving(runner);
  };
  const confirmRemove = () => {
    if (!removing) return;
    remove.mutate(removing.id, {
      onSuccess: () => {
        toast(t("servers.removed", { name: removing.name }));
        setRemoving(null);
      },
      onError: (err) => setRemoveError(errorText(t, err, "runner")),
    });
  };

  return (
    <PageSection id={`${id}-servers`} title={t("servers.title")} description={t("servers.body")}>
      <div className="space-y-4">
        {runners.isPending ? (
          <Skeleton className="h-20 w-full" />
        ) : runners.isError ? (
          <p role="alert" className="text-sm text-destructive">
            {t("servers.loadError")}
          </p>
        ) : list.length === 0 ? (
          <EmptyState
            icon={ServerIcon}
            title={t("servers.emptyTitle")}
            headingLevel="h3"
            className="px-0 py-8"
          >
            <p>{t("servers.emptyBody")}</p>
          </EmptyState>
        ) : (
          <ul aria-label={t("servers.listLabel")} className="divide-y rounded-md border">
            {list.map((runner) =>
              renaming?.id === runner.id ? (
                <li key={runner.id} className="px-3 py-2.5">
                  <InlineRename
                    label={t("servers.rename", { name: runner.name })}
                    initial={runner.name}
                    max={64}
                    pending={rename.isPending}
                    rule={t("servers.renameRule")}
                    error={renameError}
                    onSave={(name) => saveName(runner, name)}
                    onCancel={() => setRenaming(null)}
                  />
                </li>
              ) : (
                <RunnerRow
                  key={runner.id}
                  runner={runner}
                  testId={`runner-${runner.id}`}
                  actions={
                    <>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="pointer-coarse:size-11"
                        aria-label={t("servers.rename", { name: runner.name })}
                        onClick={() => startRename(runner)}
                      >
                        <PencilIcon aria-hidden="true" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="text-muted-foreground hover:text-destructive pointer-coarse:size-11"
                        aria-label={t("servers.remove", { name: runner.name })}
                        onClick={() => startRemove(runner)}
                      >
                        <Trash2Icon aria-hidden="true" />
                      </Button>
                    </>
                  }
                />
              ),
            )}
          </ul>
        )}

        <section
          aria-labelledby={`${id}-add`}
          className="space-y-3 rounded-md border bg-muted/30 px-3 py-3"
        >
          <div className="space-y-0.5">
            <h3 id={`${id}-add`} className="flex items-center gap-2 text-sm font-medium">
              <PlusIcon className="size-4 text-muted-foreground" aria-hidden="true" />
              {t("servers.addTitle")}
            </h3>
            <p className="max-w-[65ch] text-sm text-pretty text-muted-foreground">
              {t("servers.addBody")}
            </p>
          </div>
          <CodeBlock label={t("servers.runLabel")} value={RUNNER_RUN_COMMAND} />
          <CodeBlock label={t("servers.installLabel")} value={RUNNER_INSTALL_COMMAND} />
          <p className="text-xs text-pretty text-muted-foreground">
            {t("servers.checkHint", { cmd: RUNNER_CHECK_COMMAND })}
          </p>
        </section>
      </div>

      <ConfirmDialog
        open={removing !== null}
        onClose={() => setRemoving(null)}
        title={t("servers.removeTitle", { name: removing?.name ?? "" })}
        body={t("servers.removeBody")}
        confirmLabel={t("servers.removeConfirm")}
        pending={remove.isPending}
        onConfirm={confirmRemove}
      >
        {removeError ? (
          <p role="alert" className="text-sm text-destructive">
            {removeError}
          </p>
        ) : null}
      </ConfirmDialog>
    </PageSection>
  );
}
