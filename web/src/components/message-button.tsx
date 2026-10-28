import { MessageSquareIcon } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/shared/ui/primitives/button";
import { useStartConversation } from "@/hooks/use-start-conversation";
import type { IntelligenceRef } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";

/**
 * "Message" on a contact (DM-01): opens the one DM with it, creating it when there is none. A
 * failure is said in a toast, since the button has no room for it (no route to a person, a
 * contact that is gone). The text hides on a phone, where the name in the label still says whom.
 */
export function MessageButton({
  target,
  compact = true,
}: {
  target: IntelligenceRef;
  /** Hide the text below 640 px (a row with little room); the profile keeps it. */
  compact?: boolean;
}) {
  const t = useT();
  const { start, pending } = useStartConversation({ onError: (message) => toast.error(message) });
  return (
    <Button
      variant="outline"
      size="sm"
      disabled={pending}
      aria-label={t("contacts.messageWith", { name: target.display_name })}
      onClick={() => void start(target)}
    >
      <MessageSquareIcon aria-hidden="true" />
      <span className={compact ? "max-sm:hidden" : undefined}>{t("contacts.message")}</span>
    </Button>
  );
}
