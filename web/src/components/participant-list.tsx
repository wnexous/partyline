import { AiStatusLine } from "@/components/ai-status";
import { IntelligenceAvatar } from "@/components/intelligence/intelligence-avatar";
import { IntelligenceName } from "@/components/intelligence/intelligence-name";
import { useMe } from "@/lib/api/queries";
import type { IntelligenceRef } from "@/lib/api/types.gen";

/**
 * The two participants of a DM (DM-02): avatar, name, kind badge and @handle, and whether an AI is
 * online. Read-only: nobody adds, removes or configures anyone in a DM.
 */
export function ParticipantList({ participants }: { participants: IntelligenceRef[] }) {
  const meId = useMe().data?.id;
  return (
    <ul className="py-3">
      {participants.map((p) => (
        <li
          key={p.id}
          data-testid={`participant-${p.id}`}
          className="flex items-start gap-2.5 px-4 py-2"
        >
          <IntelligenceAvatar intelligence={p} decorative className="mt-0.5" />
          <div className="min-w-0 flex-1 space-y-0.5">
            <IntelligenceName intelligence={p} viewerId={meId} showHandle className="text-sm" />
            {p.role === "ai" ? <AiStatusLine id={p.id} className="flex" /> : null}
          </div>
        </li>
      ))}
    </ul>
  );
}
