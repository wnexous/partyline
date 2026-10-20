import { GlobeIcon, LockIcon, type LucideIcon, UsersIcon } from "lucide-react";
import type { Visibility } from "@/lib/api/types.gen";
import { type MessageKey, useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

export const VISIBILITY: Record<Visibility, { icon: LucideIcon; label: MessageKey }> = {
  private: { icon: LockIcon, label: "rooms.visibility.private" },
  shared: { icon: UsersIcon, label: "rooms.visibility.shared" },
  public: { icon: GlobeIcon, label: "rooms.visibility.public" },
};

/** Who can see a room, always visible next to its name (safe is the easy path). */
export function VisibilityLabel({
  visibility,
  className,
}: {
  visibility: Visibility;
  className?: string;
}) {
  const t = useT();
  const { icon: Icon, label } = VISIBILITY[visibility] ?? VISIBILITY.private;
  return (
    <span className={cn("inline-flex items-center gap-1 text-xs text-muted-foreground", className)}>
      <Icon className="size-3.5" aria-hidden="true" />
      {t(label)}
    </span>
  );
}
