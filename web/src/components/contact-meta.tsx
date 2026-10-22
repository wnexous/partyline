import { StatusDot } from "@/components/status-dot";
import { harnessLabel } from "@/lib/agents";
import type { AIContact } from "@/lib/api/types.gen";
import type { Pickable } from "@/lib/contacts";
import { useI18n } from "@/lib/i18n";
import { formatAbsolute, formatRelative } from "@/lib/time";

/**
 * What tells one AI from another beside its name: whether it is online (and, with `lastSeen`,
 * when it was last seen when it is not), the app it is open in, where it lives (`machine`: the
 * label to show for its machine; the folder as `~/…`) and, for a shared AI, whose it is. Renders
 * spans for a flex-wrap parent. `ai.online` is what to show: fold the live presence in first.
 */
export function AIMeta({
  ai,
  machine,
  lastSeen = false,
}: {
  ai: AIContact;
  machine?: string;
  lastSeen?: boolean;
}) {
  const { t, locale } = useI18n();
  const app = ai.app ? (harnessLabel(ai.app) ?? ai.app) : "";
  return (
    <>
      <StatusDot online={ai.online} withLabel />
      {lastSeen && !ai.online ? (
        ai.last_seen_at ? (
          <span className="font-mono tabular" title={formatAbsolute(ai.last_seen_at, locale)}>
            {t("contacts.lastSeen", { time: formatRelative(ai.last_seen_at, locale) })}
          </span>
        ) : (
          <span>{t("contacts.neverSeen")}</span>
        )
      ) : null}
      {app ? <span>{app}</span> : null}
      {machine ? <span>{machine}</span> : null}
      {/* A path has no spaces to break at: it may wrap anywhere rather than widen a narrow dialog. */}
      {ai.folder_label ? (
        <span className="min-w-0 font-mono [overflow-wrap:anywhere]">{ai.folder_label}</span>
      ) : null}
      {ai.owner ? <span>{t("contacts.sharedBy", { handle: ai.owner.handle })}</span> : null}
    </>
  );
}

/** The meta line of a picker row: an AI shows where it lives, a user shows nothing. */
export function PickableMeta({ pickable, online }: { pickable: Pickable; online: boolean }) {
  if (!pickable.ai) return null;
  return <AIMeta ai={{ ...pickable.ai, online }} machine={pickable.machineLabel} />;
}
