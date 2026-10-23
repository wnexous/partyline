import { PencilIcon, ServerIcon } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";
import { AIMeta } from "@/components/contact-meta";
import { ContactRow } from "@/components/contact-row";
import { InlineRename } from "@/components/inline-rename";
import { MessageButton } from "@/components/message-button";
import { Button } from "@/components/ui/button";
import { isApiError } from "@/lib/api/client";
import { useSetMachineLabel } from "@/lib/api/queries";
import type { AIContact, MachineTree, TreeNode } from "@/lib/api/types.gen";
import { machineOnlineCount } from "@/lib/contacts";
import { useI18n } from "@/lib/i18n";

/** The longest label an owner can give a machine (TREE-04), in characters. */
export const MAX_MACHINE_LABEL = 64;

/*
 * One machine of the contacts (TREE-01..04): its name (the owner's label, with the hostname
 * beside it), how many of its AIs are online, a way to rename it in place, and below it the
 * AIs as a tree: one nested under another when its folder is inside the other's folder, with
 * folders that hold no AI collapsed into one path before the name.
 */
export function MachineGroup({
  machine,
  live,
}: {
  machine: MachineTree;
  /** Folds the live presence over an AI of the tree. */
  live: (ai: AIContact) => AIContact;
}) {
  const headingId = useId();
  const [renaming, setRenaming] = useState(false);
  const counts = machineOnlineCount(machine, (ai) => live(ai).online);
  return (
    <section aria-labelledby={headingId}>
      <MachineHeader
        machine={machine}
        headingId={headingId}
        counts={counts}
        renaming={renaming}
        onRenaming={setRenaming}
      />
      <TreeList nodes={machine.nodes} live={live} />
    </section>
  );
}

function MachineHeader({
  machine,
  headingId,
  counts,
  renaming,
  onRenaming,
}: {
  machine: MachineTree;
  headingId: string;
  counts: { online: number; total: number };
  renaming: boolean;
  onRenaming: (renaming: boolean) => void;
}) {
  const { t } = useI18n();
  const rename = useSetMachineLabel();
  const [error, setError] = useState<string | null>(null);
  const name = machine.label || machine.hostname;
  const named = machine.label !== "" && machine.label !== machine.hostname;

  const close = () => {
    setError(null);
    onRenaming(false);
  };
  const save = (label: string) => {
    setError(null);
    rename.mutate(
      { hostname: machine.hostname, label },
      {
        onSuccess: close,
        onError: (err) => {
          if (isApiError(err) && err.status === 422 && err.fields.label) setError(err.fields.label);
          else toast.error(t("contacts.machineRenameFailed", { name }));
        },
      },
    );
  };

  return (
    <div className="flex min-h-12 items-center gap-3 px-4 pt-5 pb-2 md:px-8">
      <ServerIcon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      {/* While renaming the heading stays for assistive technology: the group keeps its name. */}
      <h3
        id={headingId}
        className={
          renaming
            ? "sr-only"
            : "flex min-w-0 flex-wrap items-baseline gap-x-2 text-base font-semibold tracking-[-0.01em]"
        }
      >
        <span className="min-w-0 truncate">{name}</span>{" "}
        {named ? (
          <span className="font-mono text-xs font-normal text-muted-foreground">
            {machine.hostname}
          </span>
        ) : null}
      </h3>
      {renaming ? (
        <InlineRename
          label={t("contacts.machineLabel", { hostname: machine.hostname })}
          initial={name}
          max={MAX_MACHINE_LABEL}
          allowEmpty
          placeholder={machine.hostname}
          pending={rename.isPending}
          rule={t("contacts.machineRule", { max: MAX_MACHINE_LABEL })}
          error={error}
          onSave={save}
          onCancel={close}
        />
      ) : (
        <>
          <span className="inline-flex shrink-0 items-center gap-1.5 font-mono text-xs text-muted-foreground tabular">
            {counts.online > 0 ? (
              <span aria-hidden="true" className="size-1.5 rounded-full bg-online" />
            ) : null}
            {t("contacts.onlineCount", counts)}
          </span>
          <Button
            variant="ghost"
            size="icon-xs"
            className="size-9 text-muted-foreground md:size-6"
            aria-label={t("contacts.renameMachine", { name })}
            title={t("contacts.rename")}
            onClick={() => onRenaming(true)}
          >
            <PencilIcon aria-hidden="true" />
          </Button>
        </>
      )}
    </div>
  );
}

/** The AIs of a machine, each with the ones whose folders are inside its own below it. */
function TreeList({
  nodes,
  live,
  nested = false,
}: {
  nodes: TreeNode[];
  live: (ai: AIContact) => AIContact;
  nested?: boolean;
}) {
  return (
    // A nested list hangs from a line under its parent's avatar.
    <ul className={nested ? "ml-9 border-l md:ml-12" : "divide-y border-y"}>
      {nodes.map((node) => {
        const ai = live(node.ai);
        return (
          <li key={ai.intelligence.id} className={nested ? "border-t first:border-t-0" : undefined}>
            <ContactRow
              testId={`contact-${ai.intelligence.id}`}
              className={nested ? "pr-4 pl-3 md:pr-8" : "px-4 md:px-8"}
              intelligence={ai.intelligence}
              prefix={node.path}
              meta={<AIMeta ai={ai} lastSeen />}
              actions={<MessageButton target={ai.intelligence} />}
            />
            {node.children.length > 0 ? (
              <TreeList nodes={node.children} live={live} nested />
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}
