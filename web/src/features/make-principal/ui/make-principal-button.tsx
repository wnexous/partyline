import { CrownIcon } from "lucide-react";
import { toast } from "sonner";
import { useMakePrincipal } from "@/entities/intelligence";
import { useT } from "@/shared/i18n";
import { Button } from "@/shared/ui/primitives/button";

/**
 * "Make principal" for one live parallel session of an AI (SESS-59): from now on that session is the one
 * that receives what is not addressed to a label, and the former principal becomes a parallel session.
 * The caller shows it only to whoever manages the AI and only for a labelled session that sent a
 * harness session id (`can_be_principal`); the server refuses anything else.
 */
export function MakePrincipalButton({
  agentId,
  name,
  label,
  testId,
}: {
  agentId: string;
  /** The AI's name, for the label read by assistive technology and for the toasts. */
  name: string;
  /** The label of the session to make the principal, without the colon. */
  label: string;
  testId?: string;
}) {
  const t = useT();
  const make = useMakePrincipal();
  return (
    <Button
      type="button"
      variant="outline"
      size="xs"
      data-testid={testId}
      disabled={make.isPending}
      aria-label={t("profile.makePrincipalFor", { label, name })}
      className="pointer-coarse:h-11"
      onClick={() =>
        make.mutate(
          { id: agentId, label },
          {
            onSuccess: () => toast(t("profile.principalChanged", { label, name })),
            onError: () => toast.error(t("profile.principalFailed", { name })),
          },
        )
      }
    >
      <CrownIcon aria-hidden="true" />
      {t("profile.makePrincipal")}
    </Button>
  );
}
