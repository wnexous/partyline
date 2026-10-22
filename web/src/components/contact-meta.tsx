import { StatusDot } from "@/components/status-dot";
import { harnessLabel } from "@/lib/agents";
import type { Pickable } from "@/lib/contacts";
import { useT } from "@/lib/i18n";

/**
 * What tells one AI from another beside its name in a picker: whether it is online, the app it is
 * open in, where it lives (the owner's label for the machine, the folder) and, for a shared AI,
 * whose it is. Users have nothing but a handle, so they get nothing here. Renders spans for a
 * flex-wrap parent.
 */
export function PickableMeta({ pickable, online }: { pickable: Pickable; online: boolean }) {
  const t = useT();
  const { ai, machineLabel } = pickable;
  if (!ai) return null;
  const app = ai.app ? (harnessLabel(ai.app) ?? ai.app) : "";
  return (
    <>
      <StatusDot online={online} withLabel />
      {app ? <span>{app}</span> : null}
      {machineLabel ? <span>{machineLabel}</span> : null}
      {/* A path has no spaces to break at: it may wrap anywhere rather than widen a narrow dialog. */}
      {ai.folder_label ? (
        <span className="min-w-0 font-mono [overflow-wrap:anywhere]">{ai.folder_label}</span>
      ) : null}
      {ai.owner ? <span>{t("contacts.sharedBy", { handle: ai.owner.handle })}</span> : null}
    </>
  );
}
