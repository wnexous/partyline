import { type LucideIcon, ShieldAlertIcon, ShieldCheckIcon } from "lucide-react";
import type { MessageKey } from "@/shared/i18n";
import { useI18n } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/shared/ui/primitives/tooltip";
import type { Trust } from "../model/author";

const TRUST: Record<
  Trust,
  { icon: LucideIcon; label: MessageKey; hint: MessageKey; className: string }
> = {
  trusted: {
    icon: ShieldCheckIcon,
    label: "message.trusted",
    hint: "message.trustedHint",
    className: "bg-tape text-tape-foreground ring-1 ring-tape-strong/50 ring-inset",
  },
  verified: {
    icon: ShieldCheckIcon,
    label: "message.verified",
    hint: "message.verifiedHint",
    className: "text-foreground ring-1 ring-tape-strong/50 ring-inset",
  },
  untrusted: {
    icon: ShieldAlertIcon,
    label: "message.untrusted",
    hint: "message.untrustedHint",
    className: "text-foreground outline-1 -outline-offset-1 outline-tape-strong outline-dashed",
  },
};

export function TrustBadge({ trust }: { trust: Trust }) {
  const { t } = useI18n();
  const cfg = TRUST[trust];
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className={cn(
            "inline-flex items-center gap-1 self-center rounded-[3px] px-1.5 py-px text-[0.7rem] font-semibold",
            cfg.className,
          )}
        >
          <cfg.icon className="size-3" aria-hidden="true" />
          <span>{t(cfg.label)}</span>
          <span className="sr-only">. {t(cfg.hint)}</span>
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-64 text-pretty">{t(cfg.hint)}</TooltipContent>
    </Tooltip>
  );
}
