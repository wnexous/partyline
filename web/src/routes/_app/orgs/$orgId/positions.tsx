import { createFileRoute, Navigate } from "@tanstack/react-router";
import { NetworkIcon, PlusIcon, SquarePenIcon, Trash2Icon } from "lucide-react";
import { useId, useMemo, useState } from "react";
import { toast } from "sonner";
import { EmptyState } from "@/shared/ui/molecules/empty-state";
import { InlineRename } from "@/shared/ui/molecules/inline-rename";
import { NativeSelect } from "@/shared/ui/atoms/native-select";
import { PageHeader } from "@/shared/ui/molecules/page-header";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/shared/ui/primitives/alert-dialog";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { isApiError } from "@/lib/api/client";
import {
  useCreatePosition,
  useDeletePosition,
  useOrg,
  useOrgChart,
  useUpdatePosition,
} from "@/lib/api/queries";
import {
  type ChartPosition,
  ErrorCodePositionCycle,
  type UpdatePositionRequest,
} from "@/lib/api/types.gen";
import { type Translate, useT } from "@/shared/i18n";
import { descendantsOf, type PositionNode, positionOptions, positionTree } from "@/lib/org-chart";
import { MaxPositionNameLength, PermissionAdmin } from "@/lib/orgs";

export const Route = createFileRoute("/_app/orgs/$orgId/positions")({
  component: OrgPositionsPage,
});

/*
 * The org chart's positions, for admins (POS-01, POS-02, POS-09): a tree where each position can
 * get a position under it, be renamed, be moved under another one or none, and be deleted. The
 * server decides the rest: a name must be unique, and a move that would close a loop is refused.
 * Anyone who is not an admin is sent to the chart.
 */

/** What an edit that failed says, whichever the edit was. */
function problem(t: Translate, err: unknown, org: string): string {
  if (isApiError(err)) {
    if (err.code === ErrorCodePositionCycle) return t("orgs.positionCycle");
    if (err.status === 403) return t("orgs.positionForbidden", { org });
    if (err.status === 409) return t("orgs.positionTaken");
    if (err.status === 0) return t("error.network");
    // The server's reason for a field it refuses (a name out of bounds, a parent of another organization).
    const reason = Object.values(err.fields)[0];
    if (err.status === 422 && reason) return reason;
  }
  return t("orgs.positionFailed");
}

function OrgPositionsPage() {
  const t = useT();
  const { orgId } = Route.useParams();
  // The layout has loaded the organization and shows its failures.
  const org = useOrg(orgId).data;
  const chart = useOrgChart(orgId);
  const create = useCreatePosition(orgId);
  const [addingRoot, setAddingRoot] = useState(false);
  const [rootError, setRootError] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<ChartPosition | null>(null);
  const positions = chart.data?.positions;
  const tree = useMemo(() => (positions ? positionTree(positions) : []), [positions]);
  const options = useMemo(() => positionOptions(positions ?? []), [positions]);

  if (!org) return null;
  if (org.permission !== PermissionAdmin) {
    return <Navigate to="/orgs/$orgId/chart" params={{ orgId: org.id }} replace />;
  }

  const add = (
    <Button onClick={() => setAddingRoot(true)}>
      <PlusIcon aria-hidden="true" />
      {t("orgs.addRoot")}
    </Button>
  );

  return (
    <>
      <PageHeader
        title={t("orgs.positionsTitle")}
        description={t("orgs.positionsBody", { org: org.name })}
        actions={tree.length > 0 ? add : null}
      />
      {chart.isPending ? (
        <div aria-hidden="true" className="space-y-3 px-4 py-6 md:px-8">
          <Skeleton className="h-5 w-40" />
          <Skeleton className="ml-6 h-5 w-48" />
          <Skeleton className="ml-12 h-5 w-44" />
        </div>
      ) : chart.isError && !chart.data ? (
        <div className="px-4 py-10 text-sm md:px-8" role="alert">
          <p className="text-destructive">{t("orgs.positionsError")}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => chart.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : tree.length === 0 && !addingRoot ? (
        <EmptyState icon={NetworkIcon} title={t("orgs.positionsEmptyTitle")} action={add}>
          <p>{t("orgs.positionsEmptyBody", { org: org.name })}</p>
        </EmptyState>
      ) : (
        <div className="px-4 py-4 md:px-8">
          {addingRoot ? (
            <div className="pb-3">
              <InlineRename
                label={t("orgs.positionNameLabel")}
                initial=""
                max={MaxPositionNameLength}
                pending={create.isPending}
                rule={t("orgs.positionRule")}
                error={rootError}
                onSave={(name) => {
                  setRootError(null);
                  create.mutate(
                    { name },
                    {
                      onSuccess: () => {
                        toast.success(t("orgs.positionCreated", { name }));
                        setAddingRoot(false);
                      },
                      onError: (err) => setRootError(problem(t, err, org.name)),
                    },
                  );
                }}
                onCancel={() => {
                  setRootError(null);
                  setAddingRoot(false);
                }}
              />
            </div>
          ) : null}
          <PositionList
            orgId={org.id}
            orgName={org.name}
            label={t("orgs.positionsLabel", { org: org.name })}
            nodes={tree}
            depth={0}
            all={options}
            positions={positions ?? []}
            onDelete={setDeleting}
          />
        </div>
      )}
      <DeletePositionDialog
        orgId={org.id}
        orgName={org.name}
        position={deleting}
        positions={positions ?? []}
        onClose={() => setDeleting(null)}
      />
    </>
  );
}

type Node = PositionNode<ChartPosition>;

function PositionList({
  orgId,
  orgName,
  label,
  nodes,
  depth,
  all,
  positions,
  onDelete,
}: {
  orgId: string;
  orgName: string;
  label?: string;
  nodes: Node[];
  depth: number;
  all: { id: string; name: string; depth: number }[];
  positions: readonly ChartPosition[];
  onDelete: (p: ChartPosition) => void;
}) {
  // Past four levels the rows stop moving right, so a deep chart still fits a phone.
  return (
    <ul aria-label={label} className={depth > 0 && depth < 4 ? "ml-3 border-l pl-3 md:ml-5" : ""}>
      {nodes.map((n) => (
        <PositionRow
          key={n.position.id}
          orgId={orgId}
          orgName={orgName}
          node={n}
          depth={depth}
          all={all}
          positions={positions}
          onDelete={onDelete}
        />
      ))}
    </ul>
  );
}

function PositionRow({
  orgId,
  orgName,
  node,
  depth,
  all,
  positions,
  onDelete,
}: {
  orgId: string;
  orgName: string;
  node: Node;
  depth: number;
  all: { id: string; name: string; depth: number }[];
  positions: readonly ChartPosition[];
  onDelete: (p: ChartPosition) => void;
}) {
  const t = useT();
  const nameId = useId();
  const { position, children } = node;
  const update = useUpdatePosition(orgId);
  const create = useCreatePosition(orgId);
  const [mode, setMode] = useState<"idle" | "rename" | "add">("idle");
  const [fieldError, setFieldError] = useState<string | null>(null);
  const [rowError, setRowError] = useState<string | null>(null);

  // A position can go under any other except itself and what is below it: that would close a loop.
  const below = useMemo(() => descendantsOf(positions, position.id), [positions, position.id]);
  const parents = all.filter((o) => o.id !== position.id && !below.has(o.id));
  const held = position.holders.length;

  const close = () => {
    setMode("idle");
    setFieldError(null);
  };
  const edit = (changes: UpdatePositionRequest, done: () => void) => {
    setRowError(null);
    update.mutate(
      { id: position.id, ...changes },
      {
        onSuccess: done,
        onError: (err) => {
          const text = problem(t, err, orgName);
          if (mode === "rename") setFieldError(text);
          else setRowError(text);
        },
      },
    );
  };

  return (
    <li aria-label={position.name}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
        {/* The name takes its own line on a phone, so the controls get the line below. */}
        <div className="flex min-w-0 basis-full flex-wrap items-baseline gap-x-3 gap-y-0.5 md:flex-1 md:basis-0">
          {mode === "rename" ? (
            <InlineRename
              label={t("orgs.renameLabel", { name: position.name })}
              initial={position.name}
              max={MaxPositionNameLength}
              pending={update.isPending}
              rule={t("orgs.positionRule")}
              error={fieldError}
              onSave={(name) =>
                edit({ name }, () => {
                  toast.success(t("orgs.positionRenamed", { name }));
                  close();
                })
              }
              onCancel={close}
            />
          ) : (
            <>
              <span id={nameId} className="min-w-0 font-semibold tracking-[-0.01em] break-words">
                {position.name}
              </span>
              <span className="text-xs text-muted-foreground">
                {held === 0
                  ? t("orgs.holdersNone")
                  : t(held === 1 ? "orgs.holdersOne" : "orgs.holdersMany", { count: held })}
              </span>
            </>
          )}
        </div>
        <div className="flex w-full items-center gap-1 md:w-auto md:gap-1.5">
          <NativeSelect
            aria-label={t("orgs.parentOf", { name: position.name })}
            value={position.parent_id ?? ""}
            disabled={update.isPending}
            onChange={(e) =>
              edit({ parent_id: e.target.value }, () => {
                toast(t("orgs.positionMoved", { name: position.name }));
              })
            }
            className="h-9 min-w-0 flex-1 md:w-44 md:flex-none"
          >
            <option value="">{t("orgs.noParent")}</option>
            {parents.map((o) => (
              <option key={o.id} value={o.id}>
                {o.name}
              </option>
            ))}
          </NativeSelect>
          <Button
            variant="ghost"
            size="icon"
            className="size-11 shrink-0 md:size-9"
            aria-label={t("orgs.rename", { name: position.name })}
            onClick={() => {
              setRowError(null);
              setFieldError(null);
              setMode("rename");
            }}
          >
            <SquarePenIcon aria-hidden="true" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="size-11 shrink-0 md:size-9"
            aria-label={t("orgs.addChild", { name: position.name })}
            onClick={() => {
              setRowError(null);
              setFieldError(null);
              setMode("add");
            }}
          >
            <PlusIcon aria-hidden="true" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="size-11 shrink-0 text-destructive hover:text-destructive md:size-9"
            aria-label={t("orgs.deletePosition", { name: position.name })}
            onClick={() => onDelete(position)}
          >
            <Trash2Icon aria-hidden="true" />
          </Button>
        </div>
      </div>
      {rowError ? (
        <p role="alert" className="pb-2 text-sm text-destructive">
          {rowError}
        </p>
      ) : null}
      {mode === "add" ? (
        <div className="pb-2 pl-3">
          <InlineRename
            label={t("orgs.positionNameLabel")}
            initial=""
            max={MaxPositionNameLength}
            pending={create.isPending}
            rule={t("orgs.positionRule")}
            error={fieldError}
            onSave={(name) => {
              setFieldError(null);
              create.mutate(
                { name, parent_id: position.id },
                {
                  onSuccess: () => {
                    toast.success(t("orgs.positionCreated", { name }));
                    close();
                  },
                  onError: (err) => setFieldError(problem(t, err, orgName)),
                },
              );
            }}
            onCancel={close}
          />
        </div>
      ) : null}
      {children.length > 0 ? (
        <PositionList
          orgId={orgId}
          orgName={orgName}
          nodes={children}
          depth={depth + 1}
          all={all}
          positions={positions}
          onDelete={onDelete}
        />
      ) : null}
    </li>
  );
}

/** Asks before deleting a position, saying where its holders and child positions go (POS-09). */
function DeletePositionDialog({
  orgId,
  orgName,
  position,
  positions,
  onClose,
}: {
  orgId: string;
  orgName: string;
  position: ChartPosition | null;
  positions: readonly ChartPosition[];
  onClose: () => void;
}) {
  const t = useT();
  const remove = useDeletePosition(orgId);
  const [error, setError] = useState<string | null>(null);
  const name = position?.name ?? "";
  const parent = positions.find((p) => p.id === position?.parent_id);

  const close = () => {
    setError(null);
    onClose();
  };

  return (
    <AlertDialog open={position !== null} onOpenChange={(open) => (open ? null : close())}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("orgs.deletePositionTitle", { name })}</AlertDialogTitle>
          <AlertDialogDescription>
            {parent
              ? t("orgs.deletePositionBody", { parent: parent.name })
              : t("orgs.deletePositionBodyRoot")}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            disabled={remove.isPending}
            onClick={(e) => {
              // Stay open while the request runs: a failure must leave the dialog to retry from.
              e.preventDefault();
              if (!position) return;
              setError(null);
              remove.mutate(position.id, {
                onSuccess: () => {
                  toast(t("orgs.positionDeleted", { name }));
                  close();
                },
                onError: (err) => setError(problem(t, err, orgName)),
              });
            }}
          >
            {t("orgs.deletePositionConfirm")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
