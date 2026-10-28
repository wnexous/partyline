import type { AIContact } from "@/shared/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { RelativeTime } from "@/shared/ui/atoms/relative-time";
import { StatusDot } from "@/shared/ui/atoms/status-dot";
import { appLabelOf } from "../lib/app-label";
import type { Pickable } from "../lib/contacts";

/**
 * What tells one AI from another beside its name: whether it is online (and, with `lastSeen`,
 * when it was last seen when it is not), the app it is open in, the folder it works in (as `~/…`)
 * and, for an AI somebody else hosts, who. Renders spans for a flex-wrap parent. `ai.online` is
 * what to show: fold the live presence in first.
 */
export function AIMeta({ ai, lastSeen = false }: { ai: AIContact; lastSeen?: boolean }) {
  const { t, locale } = useI18n();
  const app = ai.app ? appLabelOf(ai) : "";
  return (
    <>
      <StatusDot
        online={ai.online}
        label={ai.online ? t("presence.online") : t("presence.offline")}
        withLabel
      />
      {lastSeen && !ai.online ? (
        ai.last_seen_at ? (
          <RelativeTime value={ai.last_seen_at} locale={locale} className="font-mono tabular">
            {(time) => t("contacts.lastSeen", { time })}
          </RelativeTime>
        ) : (
          <span>{t("contacts.neverSeen")}</span>
        )
      ) : null}
      {app ? <span>{app}</span> : null}
      {/* A path has no spaces to break at: it may wrap anywhere rather than widen a narrow dialog. */}
      {ai.folder_label ? (
        <span className="min-w-0 font-mono [overflow-wrap:anywhere]">{ai.folder_label}</span>
      ) : null}
      {ai.owner ? <span>{t("contacts.hostedBy", { handle: ai.owner.handle })}</span> : null}
    </>
  );
}

/** The meta line of a picker row: an AI shows where it lives, a user shows nothing. */
export function PickableMeta({ pickable, online }: { pickable: Pickable; online: boolean }) {
  if (!pickable.ai) return null;
  return <AIMeta ai={{ ...pickable.ai, online }} />;
}
